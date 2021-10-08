// Copyright (c) 2020-2021 Larry Ruane
// Distributed under the MIT software license, see
// https://www.opensource.org/licenses/mit-license.php.

// This work has been supported by a Brink grant (https://brink.dev/)

// This program simulates a network of block miners in a proof of work system.
// You specify a network topology, and a hash rate for each miner.
// The time units are arbitrary, but seconds works well.
package main

import (
	"bufio"
	"container/heap"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"
)

var g struct {
	// Arguments:
	network       string // pathname of network topology file
	blockinterval int    // average time between blocks
	stopheight    int64  // run until this height is reached
	traceenable   bool   // show details of each sim step
	seed          int64  // random number seed, -1 means use wall-clock

	// Main simulator state:
	currenttime float64   // simulated time since start
	blocks      []block   // indexed by blockid, ordered by oldest first
	miners      []miner   // one per miner (unordered)
	eventlist   eventlist // priority queue, lowest timestamp first

	// Implementation detail simulator state:
	maxHeight   height     // greatest height any miner has reached
	baseblockid blockid    // blocks[0] corresponds to this block id
	r           *rand.Rand // for block interval calculation
	maxreorg    int        // greatest depth reorg
	trace       traceFunc  // show details of each sim step
	totalhash   int        // sum of miners' hashrates
	mined       height     // number of blocks mined up to baseblock
}

type (
	height  int64
	blockid int64
	block   struct {
		parent blockid // first block is the only block with parent = zero
		height height  // more than one block can have the same height
		miner  int     // which miner found this block
		time   float64 // time this block was mined
	}

	// The set of miners and their peers is static (at least for now).
	peer struct {
		miner int
		delay float64
	}
	miner struct {
		name     string
		index    int     // in miner[]
		hashrate int     // how much hashing power this miner has
		mined    height  // how many total blocks we've mined (including reorg)
		credit   height  // how many best-chain blocks we've mined
		peers    []peer  // outbound peers (we forward blocks to these miners)
		tip      blockid // the blockid we're trying to mine onto, initially 1
	}

	// The only event is the arrival of a block, either mined or relayed.
	event struct {
		to     int     // which miner (index) gets the block
		mining bool    // block arrival from our mining (true) or peer (false)
		when   float64 // time of block arrival
		bid    blockid // block being mined on (parent) or block from peer
	}
	eventlist []event
)

func init() {
	// Genesis block.
	g.blocks = append(g.blocks, block{
		parent: 0,
		height: 0,
		miner:  -1,
		time:   0,
	})
	g.baseblockid = 1000 // arbitrary but helps distinguish ids from heights
	g.eventlist = make([]event, 0)
	g.trace = func(format string, a ...interface{}) (n int, err error) {
		// The default trace function does nothing.
		return 0, nil
	}

	flag.StringVar(&g.network, "f", "./network", "network topology file")
	flag.IntVar(&g.blockinterval, "i", 600, "average block interval")
	flag.Int64Var(&g.stopheight, "h", 1_000_000, "stopping height")
	flag.BoolVar(&g.traceenable, "t", false, "print execution trace to stdout")
	flag.Int64Var(&g.seed, "s", 0, "random number seed, -1 to use wall-clock")
}

type traceFunc func(format string, a ...interface{}) (n int, err error)

func validblock(bid blockid) bool {
	return bid >= g.baseblockid &&
		int(bid-g.baseblockid) < len(g.blocks)
}
func getblock(bid blockid) *block {
	return &g.blocks[bid-g.baseblockid]
}
func getheight(bid blockid) height {
	return g.blocks[int(bid-g.baseblockid)].height
}

// Helper functions for the eventlist heap (priority queue)
func (e eventlist) Len() int           { return len(e) }
func (e eventlist) Less(i, j int) bool { return e[i].when < e[j].when }
func (e eventlist) Swap(i, j int)      { e[i], e[j] = e[j], e[i] }
func (e *eventlist) Push(x interface{}) {
	*e = append(*e, x.(event))
}
func (e *eventlist) Pop() interface{} {
	old := *e
	n := len(old)
	x := old[n-1]
	*e = old[0 : n-1]
	return x
}

// Relay a newly-discovered block (either mined or relayed to us) to our peers.
// This sends a message to the peer we received the block from (if it's one
// of our peers), but that's okay, it will be ignored.
func relay(mi int, newblockid blockid) {
	m := &g.miners[mi]
	for _, p := range m.peers {
		// Improve simulator efficiency by not relaying blocks
		// that are certain to be ignored.
		if getheight(g.miners[p.miner].tip) < getheight(newblockid) {
			heap.Push(&g.eventlist, event{
				to:     p.miner,
				mining: false,
				when:   g.currenttime + p.delay,
				bid:    newblockid})
		}
	}
}

// Start mining on top of the given existing block
func startMining(mi int, bid blockid) {
	m := &g.miners[mi]
	// We'll mine on top of blockid
	m.tip = bid

	// Schedule an event for when our "mining" will be done.
	solvetime := -math.Log(1.0-rand.Float64()) *
		float64(g.blockinterval*g.totalhash) / float64(m.hashrate)

	heap.Push(&g.eventlist, event{
		to:     mi,
		mining: true,
		when:   g.currenttime + solvetime,
		bid:    bid})
	g.trace("%.3f %s start-on %d height %d mined %d credit %d solve %.2f\n",
		g.currenttime, m.name, bid, getheight(bid),
		m.mined, m.credit, solvetime)
}

// Remove un-needed blocks, give credits to miners.
func cleanBlocks() {
	// Find the minimum height that any miner is at.
	var minheight height
	for mi, m := range g.miners {
		h := getheight(m.tip)
		if mi == 0 || minheight > h {
			minheight = h
		}
	}

	// Move down from all tips until they're at the same (minimum) height.
	blockAtSameHeight := make([]blockid, len(g.miners))
	for i, m := range g.miners {
		blockAtSameHeight[i] = m.tip
		for getheight(blockAtSameHeight[i]) > minheight {
			blockAtSameHeight[i] = getblock(blockAtSameHeight[i]).parent
		}
	}
	// Find the block that all tips are based on (oldest branch point).
	for {
		// Determine if all the blockAtSameHeight[] are equal.
		var i int
		for i = 1; i < len(g.miners); i++ {
			if blockAtSameHeight[i] != blockAtSameHeight[0] {
				break
			}
		}
		if i >= len(g.miners) {
			// Yes, they are all equal.
			break
		}
		// Everyone move down one and try again.
		for i = 0; i < len(g.miners); i++ {
			blockAtSameHeight[i] = getblock(blockAtSameHeight[i]).parent
		}
	}
	newbaseblockid := blockAtSameHeight[0]

	// Give credits to miners (these blocks can't be reorged away).
	b := getblock(newbaseblockid)
	for b != &g.blocks[0] {
		g.miners[b.miner].credit++
		b = getblock(b.parent)
	}
	// Increment the number of blocks mined per miner.
	for i := blockid(0); i < newbaseblockid-g.baseblockid; i++ {
		b := g.blocks[i]
		// don't include the genesis block
		if b.height > 0 {
			g.mined++
		}
	}

	// Remove older blocks that are no longer relevant.
	g.blocks = g.blocks[newbaseblockid-g.baseblockid:]
	g.baseblockid = newbaseblockid
}

func main() {
	flag.Parse()
	var err error
	networkfile, err := os.Open(g.network)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open failed:", err)
		os.Exit(1)
	}
	if g.traceenable {
		g.trace = fmt.Printf
	}
	if g.seed == -1 {
		g.seed = time.Now().UnixNano()
	}
	if g.seed > 0 {
		rand.Seed(g.seed)
	}
	minerMap := make(map[string][]string, 0)
	minerIndex := make(map[string]int, 0)
	i := 0
	scan := bufio.NewScanner(networkfile)
	for scan.Scan() { // each line
		// Each line is a miner name, hashrate, then a list of pairs of
		// peer name and delay (time to send to that peer)
		fields := strings.Fields(scan.Text())
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "#" {
			continue
		}
		if _, ok := minerMap[fields[0]]; ok {
			fmt.Fprintln(os.Stderr, "duplicate miner name:", fields[0])
			os.Exit(1)
		}
		minerMap[fields[0]] = fields[1:]
		minerIndex[fields[0]] = i
		i++
	}
	if len(minerMap) == 0 {
		fmt.Fprintln(os.Stderr, "no miners")
		os.Exit(1)
	}

	// Set up (static) set of miners.
	g.miners = make([]miner, i)
	for k, v := range minerMap {
		// v is a slice of whitespace-separated tokens (on a line)
		hr, err := strconv.Atoi(v[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "bad hashrate:", v[0], err)
			os.Exit(1)
		}
		if hr <= 0 {
			fmt.Fprintln(os.Stderr, "hashrate must be greater than zero:", v[0])
			os.Exit(1)
		}
		g.totalhash += hr
		m := miner{hashrate: hr}
		m.name = k
		m.index = minerIndex[k]
		v = v[1:]
		if (len(v) % 2) > 0 {
			fmt.Fprintln(os.Stderr, "bad peer delay pairs:", k, v)
			os.Exit(1)
		}
		for len(v) > 0 {
			if _, ok := minerIndex[v[0]]; !ok {
				fmt.Fprintln(os.Stderr, "no such miner:", v[0])
				os.Exit(1)
			}
			delay, err := strconv.ParseFloat(v[1], 64)
			if err != nil {
				fmt.Fprintln(os.Stderr, "bad delay:", v[1], err)
				os.Exit(1)
			}
			m.peers = append(m.peers, peer{minerIndex[v[0]], delay})
			v = v[2:]
		}
		g.miners[m.index] = m
	}

	// Start all miners off mining their first blocks.
	for mi := range g.miners {
		// Begin mining on blockid 1 (our genesis block, height zero).
		startMining(mi, g.baseblockid)
	}

	// Main event loop
	for g.maxHeight < height(g.stopheight) {
		if g.maxHeight%10000 == 0 {
			cleanBlocks()
		}
		ev := heap.Pop(&g.eventlist).(event)
		g.currenttime = ev.when
		mi := ev.to
		m := &g.miners[mi]
		height := getheight(m.tip)
		if ev.mining {
			// We mined a block (unless this is a stale event).
			if ev.bid != m.tip {
				// This is a stale mining event, ignore it (we should
				// still have an active mining event outstanding).
				continue
			}
			m.mined++
			ev.bid = g.baseblockid + blockid(len(g.blocks))
			height++
			if g.maxHeight < height {
				g.maxHeight = height
			}
			g.blocks = append(g.blocks, block{
				parent: m.tip,
				height: height,
				miner:  mi,
				time:   g.currenttime,
			})
			g.trace("%.3f %s mined-newid %d on %d height %d\n",
				g.currenttime, m.name, ev.bid, m.tip, height)
		} else {
			// Block received from a peer (but could be a stale message).
			if !validblock(ev.bid) || getheight(ev.bid) <= height {
				// We're already mining on a block that's at least as good.
				continue
			}
			// This block is better, switch to it, first compute reorg depth.
			g.trace("%.3f %s received-switch-to %d\n",
				g.currenttime, m.name, ev.bid)
			c := getblock(m.tip)  // current block we're mining on
			t := getblock(ev.bid) // to block (switching to)
			// Move back on the "to" (better) chain until even with current.
			for t.height > c.height {
				t = getblock(t.parent)
			}
			// From the same height, count blocks until these branches meet.
			reorg := 0
			for t != c {
				reorg++
				t = getblock(t.parent)
				c = getblock(c.parent)
			}
			if reorg > 0 {
				g.trace("%.3f %s reorg %d maxreorg %d\n",
					g.currenttime, m.name, reorg, g.maxreorg)
			}
			if g.maxreorg < reorg {
				g.maxreorg = reorg
			}
		}
		relay(mi, ev.bid)
		startMining(mi, ev.bid)
	}
	cleanBlocks()
	var bestchainblocks height = g.blocks[0].height
	var staleblocks height = g.mined - bestchainblocks
	fmt.Printf("%-20s %14d\n", "seed-arg", g.seed)
	fmt.Printf("%-20s %14d\n", "block-interval-arg", g.blockinterval)
	fmt.Printf("%-20s %14d\n", "stopheight-arg", g.stopheight)
	fmt.Printf("%-20s %14d\n", "total-hashrate-arg", g.totalhash)
	fmt.Printf("%-20s %14d\n", "mined-blocks", g.mined)
	fmt.Printf("%-20s %14.3f\n", "total-simtime", g.blocks[0].time)
	fmt.Printf("%-20s %14.3f\n", "ave-block-time",
		float64(g.blocks[0].time)/float64(bestchainblocks))
	fmt.Printf("%-20s %14d\n", "stale-blocks", staleblocks)
	fmt.Printf("%-20s %14.2f%%\n", "stale-rate",
		float64(staleblocks*100)/float64(g.mined))
	fmt.Printf("%-20s %14d\n", "max-reorg-depth", g.maxreorg)
	for _, m := range g.miners {
		fmt.Printf("miner %-13s  hashrate-arg %6d %6.2f%% ", m.name,
			m.hashrate, float64(m.hashrate*100)/float64(g.totalhash))
		fmt.Printf("blocks %6.2f%% ",
			float64(m.credit*100)/float64(bestchainblocks))
		fmt.Printf("stale-rate %6.2f%%",
			float64((m.mined-m.credit)*100)/float64(m.mined))
		fmt.Println("")
	}
}
