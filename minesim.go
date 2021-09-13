// Copyright (c) 2020 Larry Ruane
// Distributed under the MIT software license, see
// https://www.opensource.org/licenses/mit-license.php.
//
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
	network       string  // pathname of network topology file
	blockinterval float64 // average time between blocks
	repetitions   int     // number of simulation steps
	traceenable   bool    // show details of each sim step
	seed          int64   // random number seed, -1 means use wall-clock

	// Main simulator state:
	currenttime float64   // simulated time since start
	blocks      []block   // indexed by blockid, ordered by oldest first
	miners      []miner   // one per miner (unordered)
	eventlist   eventlist // priority queue, lowest timestamp first

	// Implementation detail simulator state:
	baseblockid blockid         // blocks[0] corresponds to this block id
	tips        map[blockid]int // actively being mined on, for pruning
	r           *rand.Rand      // for block interval calculation
	maxreorg    int             // greatest depth reorg
	trace       traceFunc       // show details of each sim step
	totalhash   float64         // sum of miners' hashrates
}

type (
	height  int64
	blockid int64
	block   struct {
		parent blockid // first block is the only block with parent = zero
		height height  // more than one block can have the same height
		miner  int     // which miner found this block
	}

	// The set of miners and their peers is static (at least for now).
	peer struct {
		miner int
		delay float64
	}
	miner struct {
		name     string
		index    int     // in miner[]
		hashrate float64 // how much hashing power this miner has
		mined    int     // how many total blocks we've mined (including reorg)
		credit   int     // how many best-chain blocks we've mined
		peer     []peer  // outbound peers (we forward blocks to these miners)
		tip      blockid // the blockid we're trying to mine onto, initially 1
	}

	// The only event is the arrival of a block, either mined or relayed.
	event struct {
		to     int     // which miner gets the block
		mining bool    // block arrival from mining (true) or peer (false)
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
		miner:  -1})
	g.baseblockid = 1000 // arbitrary but helps distinguish ids from heights
	g.tips = make(map[blockid]int, 0)
	g.eventlist = make([]event, 0)
	g.trace = func(format string, a ...interface{}) (n int, err error) {
		// The default trace function does nothing.
		return 0, nil
	}

	flag.StringVar(&g.network, "f", "./network", "network topology file")
	flag.Float64Var(&g.blockinterval, "i", 300, "average block interval")
	flag.IntVar(&g.repetitions, "r", 1_000_000, "number of simulation steps")
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

func stopMining(mi int) {
	m := &g.miners[mi]
	g.tips[m.tip]--
	if g.tips[m.tip] == 0 {
		delete(g.tips, m.tip)
	}
}

// Relay a newly-discovered block (either mined or relayed to us) to our peers.
// This sends a message to the peer we received the block from (if it's one
// of our peers), but that's okay, it will be ignored.
func relay(mi int, newblockid blockid) {
	m := &g.miners[mi]
	for _, p := range m.peer {
		// Improve simulator efficiency by not relaying blocks
		// that are certain to be ignored.
		if getheight(g.miners[p.miner].tip) < getheight(newblockid) {
			// TODO jitter this delay, or sometimes fail to forward?
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
	g.tips[m.tip]++

	// Schedule an event for when our "mining" will be done.
	solvetime := -math.Log(1.0-rand.Float64()) *
		g.blockinterval * g.totalhash / m.hashrate

	heap.Push(&g.eventlist, event{
		to:     mi,
		mining: true,
		when:   g.currenttime + solvetime,
		bid:    bid})
	g.trace("%.3f %s start-on %d height %d mined %d credit %d solve %.2f\n",
		g.currenttime, m.name, bid, getheight(bid),
		m.mined, m.credit, solvetime)
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
		hr, err := strconv.ParseFloat(v[0], 64)
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
			m.peer = append(m.peer, peer{minerIndex[v[0]], delay})
			v = v[2:]
		}
		g.miners[m.index] = m
	}

	// Start all miners off mining their first blocks.
	for mi := range g.miners {
		// Begin mining on blockid 1 (our genesis block, height zero).
		startMining(mi, g.baseblockid)
	}

	// Start of main loop.
	for rep := 0; rep < g.repetitions; rep++ {
		if len(g.tips) == 1 && len(g.blocks) > 1 {
			// Since all miners are building on the same tip, the blocks from
			// the tip to the base can't be reorged away, so we can remove
			// them, but give credit for these mined blocks as we do.
			newbaseblockid := g.miners[0].tip
			b := getblock(newbaseblockid)
			for b != &g.blocks[0] {
				g.miners[b.miner].credit++
				b = getblock(b.parent)
			}
			// Clean up (prune) unneeded blocks.
			g.blocks = []block{*getblock(newbaseblockid)}
			g.baseblockid = newbaseblockid
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
			stopMining(mi)
			ev.bid = g.baseblockid + blockid(len(g.blocks))
			height++
			g.blocks = append(g.blocks, block{
				parent: m.tip,
				height: height,
				miner:  mi})
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
			stopMining(mi)
		}
		relay(mi, ev.bid)
		startMining(mi, ev.bid)
	}
	var totalblocks int
	var minedblocks int
	var totalstale int
	for _, m := range g.miners {
		totalblocks += m.credit
		minedblocks += m.mined
		totalstale += m.mined - m.credit
	}
	fmt.Printf("seed-arg %d\n", g.seed)
	if g.blockinterval > 0 {
		fmt.Printf("block-interval-arg %.2f\n", g.blockinterval)
	}
	fmt.Printf("mined-blocks %d\n",
		minedblocks)
	fmt.Printf("height %d %.2f%%\n", totalblocks,
		float64(totalblocks)*100/float64(minedblocks))
	fmt.Printf("total-simtime %.2f\n",
		g.currenttime)
	fmt.Printf("ave-block-time %.2f\n",
		float64(g.currenttime)/float64(totalblocks))
	fmt.Printf("total-hashrate-arg %.2f\n",
		g.totalhash)
	fmt.Printf("total-stale %d\n",
		totalstale)
	fmt.Printf("max-reorg-depth %d\n", g.maxreorg)
	fmt.Printf("baseblockid %d\n", g.baseblockid)
	fmt.Printf("repetitions-arg %d\n", g.repetitions)
	for _, m := range g.miners {
		fmt.Printf("miner %s hashrate-arg %.2f %.2f%% ", m.name,
			m.hashrate, m.hashrate*100/g.totalhash)
		fmt.Printf("blocks %.2f%% ",
			float64(m.credit*100)/float64(totalblocks))
		fmt.Printf("stale %.2f%%",
			float64((m.mined-m.credit)*100)/float64(m.mined))
		fmt.Println("")
	}
}
