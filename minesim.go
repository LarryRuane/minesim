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
	currenttime   float64       // simulated time since start
	r             *rand.Rand    // for block interval calculation
	blocks        []block_t     // ordered by oldest first
	baseblockid   int64         // blocks[0] corresponds to this block id
	tips          map[int64]int // for pruning
	bestblockid   int64         // tip has a max height (may not be unique)
	miners        []miner_t     // one per miner (unordered)
	eventlist     eventlist_t   // priority queue, lowest timestamp first
	difficulty    float64       // increase average block time
	blockinterval float64       // target, if zero, use difficulty
	totalhash     float64       // sum of miners' hashrates
	repetitions   int           // number of simulation steps
	traceenable   bool          // show details of each sim step
	trace         trace_t       // show details of each sim step
	seed          int64         // random number seed, -1 means use wall-clock
}

func init() {
	// Our genesis block has height 1.
	g.blocks = append(g.blocks, block_t{
		parent: 0,
		height: 0,
		miner:  -1})
	g.baseblockid = 1
	g.tips = make(map[int64]int, 0)
	g.bestblockid = 1
	g.eventlist = make([]event_t, 0)
	g.trace = func(format string, a ...interface{}) (n int, err error) {
		return 0, nil
	}

	flag.BoolVar(&g.traceenable, "t", false, "print execution trace to stdout")
	flag.IntVar(&g.repetitions, "r", 20, "number of simulation steps")
	flag.Float64Var(&g.difficulty, "d", -1, "difficulty")
	flag.Float64Var(&g.blockinterval, "i", -1, "average block interval")
	flag.Int64Var(&g.seed, "s", 0, "random number seed, -1 to use wall-clock")
}

type trace_t func(format string, a ...interface{}) (n int, err error)

type block_t struct {
	parent int64 // first block is the only block with parent = zero
	height int   // more than one block can have the same height
	miner  int   // which miner found this block
}

func getblock(blockid int64) *block_t {
	return &g.blocks[blockid-g.baseblockid]
}
func getheight(blockid int64) int {
	return g.blocks[blockid-g.baseblockid].height
}

// The set of miners is static (at least for now).
type (
	peer_t struct {
		miner int
		delay float64
	}
	miner_t struct {
		name     string
		index    int      // in miner[]
		hashrate float64  // how much hashing power this miner has
		mined    int      // how many blocks we've mined total (including reorg)
		credit   int      // how many blocks we've mined we get credit for
		peer     []peer_t // outbound peers (we forward blocks to these miners)
		current  int64    // the blockid we're trying to mine onto, initially 1
	}
)

// The only event is the arrival of a block at a miner; if the block id is
// negative, that means this miner mined a new block on this blockid.
type (
	event_t struct {
		when    float64 // time of block arrival
		to      int     // which miner gets the block
		blockid int64   // >0: arriving on p2p, <0: block we're mining on
	}
	eventlist_t []event_t
)

func (e eventlist_t) Len() int           { return len(e) }
func (e eventlist_t) Less(i, j int) bool { return e[i].when < e[j].when }
func (e eventlist_t) Swap(i, j int)      { e[i], e[j] = e[j], e[i] }
func (e *eventlist_t) Push(x interface{}) {
	*e = append(*e, x.(event_t))
}
func (e *eventlist_t) Pop() interface{} {
	old := *e
	n := len(old)
	x := old[n-1]
	*e = old[0 : n-1]
	return x
}

func stopMining(mi int) {
	m := &g.miners[mi]
	g.tips[m.current]--
	if g.tips[m.current] == 0 {
		delete(g.tips, m.current)
	}
}

// Relay a newly-discovered block (either mined or relayed to us) to our peers.
func relay(mi int, newblockid int64) {
	m := &g.miners[mi]
	for _, p := range m.peer {
		// jitter this delay, or sometimes fail to forward?
		heap.Push(&g.eventlist, event_t{
			when:    g.currenttime + p.delay,
			to:      p.miner,
			blockid: newblockid})
	}
}

// Start mining on top of the given existing block
func startMining(mi int, blockid int64) {
	m := &g.miners[mi]
	// We'll mine on top of blockid
	m.current = blockid
	g.tips[m.current]++

	// Schedule an event for when our "mining" will be done
	// (the larger the hashrate, the smaller the delay).
	solvetime := -math.Log(1.0-rand.Float64()) / m.hashrate
	if g.blockinterval == -1 {
		solvetime *= 1e6 * g.difficulty
	} else {
		solvetime *= g.blockinterval * g.totalhash
	}
	// negative blockid means mining (not p2p)
	heap.Push(&g.eventlist, event_t{
		when:    g.currenttime + solvetime,
		to:      mi,
		blockid: -blockid})
	g.trace("%.2f %s start-on %d height %d nmined %d credit %d solve %.2f\n",
		g.currenttime, m.name, blockid, getheight(blockid),
		m.mined, m.credit, solvetime)
}

func main() {
	flag.Parse()
	if g.difficulty != -1 && g.blockinterval != -1 {
		fmt.Fprintln(os.Stderr,
			"can't specify both -difficulty and -blockinterval")
	}
	var err error
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "network-file required")
		os.Exit(1)
	}
	f, err := os.Open(flag.Arg(0))
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
	scan := bufio.NewScanner(f)
	for scan.Scan() { // each line
		// Each line is a hashrate, then a list of pairs of
		// client id and delay (time to send to that client)
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
	g.miners = make([]miner_t, i)
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
		m := miner_t{hashrate: hr}
		m.name = k
		m.index = minerIndex[k]
		v = v[1:]
		if (len(v) % 2) > 0 {
			fmt.Fprintln(os.Stderr, "bad client delay pairs:", k, v)
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
			m.peer = append(m.peer, peer_t{minerIndex[v[0]], delay})
			v = v[2:]
		}
		g.miners[m.index] = m
	}

	// Start all miners off mining their first blocks.
	for mi := range g.miners {
		// begin mining on blockid 1 (our genesis block)
		startMining(mi, 1)
	}

	// should this be based instead on time?
	for i := 0; i < g.repetitions; i++ {
		// clean up unneeded blocks
		if len(g.tips) == 1 {
			for {
				if _, ok := g.tips[g.baseblockid]; ok {
					break
				}
				g.baseblockid++
				g.blocks = g.blocks[1:]
			}
		}
		event := heap.Pop(&g.eventlist).(event_t)
		g.currenttime = event.when
		mi := event.to
		m := &g.miners[mi]
		if event.blockid > 0 {
			// This block is from a peer, see if it's useful
			// (the peer has already created event.blockid).
			if event.blockid >= g.baseblockid &&
				getheight(m.current) < getheight(event.blockid) {
				// incoming block is better, switch to it
				stopMining(mi)
				g.trace("%.2f %s received-switch-to %d\n",
					g.currenttime, g.miners[mi].name, event.blockid)
				relay(mi, event.blockid)
				startMining(mi, event.blockid)
			}
			continue
		}
		// We mined a block (unless this is a stale event)
		event.blockid = -event.blockid
		if event.blockid != m.current {
			// This is a stale mining event, ignore it (we should
			// still have an active event outstanding).
			continue
		}
		m.mined++
		stopMining(mi)
		newblockid := g.baseblockid + int64(len(g.blocks))
		g.trace("%.2f %s mined-newid %d height %d\n",
			g.currenttime, g.miners[mi].name,
			newblockid, getheight(m.current)+1)
		g.blocks = append(g.blocks, block_t{
			parent: m.current,
			height: getheight(m.current) + 1,
			miner:  mi})
		relay(mi, newblockid)
		prev := m.current
		startMining(mi, newblockid)
		if prev == g.bestblockid {
			// We're extending the best chain.
			g.trace("%.2f %s extend %d\n",
				g.currenttime, g.miners[mi].name, prev)
			g.bestblockid = m.current
			m.credit++
			continue
		}
		if getheight(m.current) <= getheight(g.bestblockid) {
			// we're mining on a non-best branch
			g.trace("%.2f %s nonbest %d\n",
				g.currenttime, g.miners[mi].name, prev)
			continue
		}
		// The current chain now has one more block than what was
		// the best chain (reorg), adjust credits.
		g.trace("%.2f %s reorg %d %d\n",
			g.currenttime, g.miners[mi].name, g.bestblockid, m.current)
		m.credit++
		dec := g.bestblockid // decrement credits on this branch
		inc := prev          // increment credits on this branch
		for dec != inc {
			db := getblock(dec)
			ib := getblock(inc)
			g.miners[db.miner].credit--
			g.miners[ib.miner].credit++
			g.trace("%.2f reorg height %d dec %s %d inc %s %d\n",
				g.currenttime, db.height,
				g.miners[db.miner].name, g.miners[db.miner].credit,
				g.miners[ib.miner].name, g.miners[ib.miner].credit)
			dec = db.parent
			inc = ib.parent
		}
		g.bestblockid = m.current
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
	if g.difficulty > 0 {
		fmt.Printf("difficulty-arg %.2f\n", g.difficulty)
	}
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
	fmt.Printf("baseblockid %d\n", g.baseblockid)
	fmt.Printf("repetitions %d\n", g.repetitions)
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
