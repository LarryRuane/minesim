// Copyright (c) 2020-present Larry Ruane
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
	prunedepth    int    // bound memory if the chains never converge

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
	totalhash   int        // sum of miners' hashrates
	mined       height     // number of blocks mined up to baseblock
	pruned      bool       // has prunedepth ever had to step in?
}

// How much the tallest chain must grow before we finalize old blocks again.
const cleanInterval = 10000

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
		name       string
		index      int     // in miner[]
		hashrate   int     // how much hashing power this miner has
		mined      height  // how many total blocks we've mined (for tracing)
		minedfinal height  // of those, how many can no longer be reorged
		credit     height  // how many best-chain blocks we've mined
		peers      []peer  // outbound peers (we forward blocks to these miners)
		tip        blockid // the blockid we're trying to mine onto, initially 1
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

	flag.StringVar(&g.network, "f", "./network", "network topology file")
	flag.IntVar(&g.blockinterval, "i", 600, "average block interval")
	flag.Int64Var(&g.stopheight, "h", 1_000_000, "stopping height")
	flag.BoolVar(&g.traceenable, "t", false, "print execution trace to stdout")
	flag.Int64Var(&g.seed, "s", 0, "random number seed, -1 to use wall-clock")
	flag.IntVar(&g.prunedepth, "p", 100_000,
		"bound memory if the miners' chains never converge, 0 for no limit")
}

// Report a problem that doesn't stop the simulation. This goes to stderr so
// that the results on stdout stay clean enough to pipe into another program.
func warn(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "minesim: warning: "+format+"\n", a...)
}

func fatal(a ...interface{}) {
	fmt.Fprintln(os.Stderr, append([]interface{}{"minesim:"}, a...)...)
	os.Exit(1)
}

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
	newheight := getheight(newblockid)
	for _, p := range m.peers {
		// Improve simulator efficiency by not relaying blocks
		// that are certain to be ignored. A miner's tip height never
		// decreases, so a peer already this high will never want this block.
		if getheight(g.miners[p.miner].tip) < newheight {
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
	solvetime := -math.Log(1.0-g.r.Float64()) *
		float64(g.blockinterval*g.totalhash) / float64(m.hashrate)

	heap.Push(&g.eventlist, event{
		to:     mi,
		mining: true,
		when:   g.currenttime + solvetime,
		bid:    bid})
	if g.traceenable {
		fmt.Printf("%.3f %s start-on %d height %d mined %d credit %d solve %.2f\n",
			g.currenttime, m.name, bid, getheight(bid),
			m.mined, m.credit, solvetime)
	}
}

// How many blocks we back up to switch from one chain to another.
func reorgDepth(fromtip, totip blockid) int {
	// Move back on the "to" (better) chain until even with current.
	for getheight(totip) > getheight(fromtip) {
		totip = getblock(totip).parent
	}
	// From the same height, count blocks until these branches meet.
	depth := 0
	for totip != fromtip {
		depth++
		totip = getblock(totip).parent
		fromtip = getblock(fromtip).parent
	}
	return depth
}

// Count and discard every block below newbase, giving credits to miners.
//
// Note the asymmetry: newbase itself is credited here (it's certainly on the
// best chain) but is not counted as mined, because it isn't being dropped
// yet. It gets counted by the next call, when it does drop. The very last
// base block is counted once by main().
func setBase(newbase blockid) {
	for bid := newbase; bid != g.baseblockid; {
		b := getblock(bid)
		g.miners[b.miner].credit++
		bid = b.parent
	}

	// Count the blocks about to be dropped, best-chain and stale alike,
	// but not the genesis block, which nobody mined.
	for i := blockid(0); i < newbase-g.baseblockid; i++ {
		b := g.blocks[i]
		if b.height > 0 {
			g.mined++
			g.miners[b.miner].minedfinal++
		}
	}

	// Remove older blocks that are no longer relevant.
	g.blocks = g.blocks[newbase-g.baseblockid:]
	g.baseblockid = newbase
}

// Finalize and discard the blocks that can no longer be reorged away.
// Everything below the point where all the miners' chains agree is settled,
// so it can be counted, credited, and forgotten.
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
	setBase(blockAtSameHeight[0])
}

// Bound memory when the miners' chains never converge.
//
// cleanBlocks() can only discard blocks that every miner agrees on, so a
// single miner that is permanently far behind -- almost always one with no
// inbound peers, which lintNetwork() warns about -- stops anything from ever
// being freed, and memory grows without limit. When that happens we drop the
// oldest blocks anyway and move the stragglers onto the new base, as though
// they had finally heard about the better chain.
func forcePrune() {
	// Back off half the limit from the tallest tip, so that this doesn't
	// have to run again on the very next block.
	newbase := g.miners[0].tip
	for _, m := range g.miners {
		if getheight(m.tip) > getheight(newbase) {
			newbase = m.tip
		}
	}
	target := getheight(newbase) - height(g.prunedepth/2)
	for getheight(newbase) > target && newbase != g.baseblockid {
		newbase = getblock(newbase).parent
	}
	if newbase == g.baseblockid {
		// Nothing to drop; every branch is short but there are many.
		return
	}

	newheight := getheight(newbase)
	for mi := range g.miners {
		bid := g.miners[mi].tip
		for getheight(bid) > newheight {
			bid = getblock(bid).parent
		}
		if bid != newbase {
			if !g.pruned {
				g.pruned = true
				warn("miner %s fell more than %d blocks behind, so -p moved "+
					"it onto the best chain to keep memory bounded; its "+
					"statistics will not be meaningful",
					g.miners[mi].name, g.prunedepth)
			}
			startMining(mi, newbase)
		}
	}
	setBase(newbase)
}

// Read the network topology file, filling in g.miners.
//
// Each line is a miner name, its hashrate, and then a list of pairs of peer
// name and the delay to send a block to that peer. Empty lines and lines
// whose first token starts with "#" are ignored.
func readNetwork(path string) {
	networkfile, err := os.Open(path)
	if err != nil {
		fatal("open failed:", err)
	}
	defer networkfile.Close()

	var lines [][]string
	scan := bufio.NewScanner(networkfile)
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		lines = append(lines, fields)
	}
	if len(lines) == 0 {
		fatal("no miners")
	}

	// Number the miners first, because a line can name a peer that isn't
	// defined until later in the file.
	minerIndex := make(map[string]int, len(lines))
	for _, fields := range lines {
		if _, ok := minerIndex[fields[0]]; ok {
			fatal("duplicate miner name:", fields[0])
		}
		minerIndex[fields[0]] = len(minerIndex)
	}

	g.miners = make([]miner, len(lines))
	for _, fields := range lines {
		name, v := fields[0], fields[1:]
		if len(v) == 0 {
			fatal("missing hashrate:", name)
		}
		hr, err := strconv.Atoi(v[0])
		if err != nil {
			fatal("bad hashrate:", v[0], err)
		}
		if hr <= 0 {
			fatal("hashrate must be greater than zero:", v[0])
		}
		g.totalhash += hr
		m := miner{name: name, index: minerIndex[name], hashrate: hr}
		v = v[1:]
		if (len(v) % 2) > 0 {
			fatal("bad peer delay pairs:", name, v)
		}
		for len(v) > 0 {
			if _, ok := minerIndex[v[0]]; !ok {
				fatal("no such miner:", v[0])
			}
			delay, err := strconv.ParseFloat(v[1], 64)
			if err != nil {
				fatal("bad delay:", v[1], err)
			}
			m.peers = append(m.peers, peer{minerIndex[v[0]], delay})
			v = v[2:]
		}
		g.miners[m.index] = m
	}
}

// Warn about configuration mistakes that give confusing results.
func lintNetwork() {
	inbound := make([]int, len(g.miners))
	for _, m := range g.miners {
		seen := make(map[int]bool, len(m.peers))
		for _, p := range m.peers {
			inbound[p.miner]++
			if p.miner == m.index {
				warn("miner %s lists itself as a peer", m.name)
			} else if seen[p.miner] {
				warn("miner %s lists peer %s more than once",
					m.name, g.miners[p.miner].name)
			}
			seen[p.miner] = true
		}
	}
	for _, m := range g.miners {
		if inbound[m.index] == 0 {
			warn("miner %s has no inbound peers, so it will never hear about "+
				"anyone else's blocks; it will mine its own private chain "+
				"and the statistics will come out as NaN", m.name)
		}
	}
}

func main() {
	flag.Parse()
	if g.blockinterval <= 0 {
		fatal("-i must be greater than zero")
	}
	if g.stopheight <= 0 {
		fatal("-h must be greater than zero")
	}
	if g.prunedepth < 0 {
		fatal("-p must not be negative")
	}
	if g.seed == -1 {
		g.seed = time.Now().UnixNano()
	}
	// Use our own random source rather than the global one: since Go 1.24,
	// rand.Seed() is a no-op and the global source is seeded randomly, which
	// silently made -s do nothing at all.
	g.r = rand.New(rand.NewSource(g.seed))

	readNetwork(g.network)
	lintNetwork()

	// Start all miners off mining their first blocks.
	for mi := range g.miners {
		// Begin mining on blockid 1 (our genesis block, height zero).
		startMining(mi, g.baseblockid)
	}

	// Main event loop
	cleanedat := height(0) // the height when we last called cleanBlocks()
	for g.maxHeight < height(g.stopheight) {
		if g.maxHeight-cleanedat >= cleanInterval {
			cleanedat = g.maxHeight
			cleanBlocks()
		}
		if g.prunedepth > 0 && len(g.blocks) > g.prunedepth {
			forcePrune()
		}
		ev := heap.Pop(&g.eventlist).(event)
		g.currenttime = ev.when
		mi := ev.to
		m := &g.miners[mi]
		bid := ev.bid
		if ev.mining {
			// We mined a block (unless this is a stale event).
			if bid != m.tip {
				// This is a stale mining event, ignore it (we should
				// still have an active mining event outstanding).
				continue
			}
			m.mined++
			newheight := getheight(m.tip) + 1
			if g.maxHeight < newheight {
				g.maxHeight = newheight
			}
			bid = g.baseblockid + blockid(len(g.blocks))
			g.blocks = append(g.blocks, block{
				parent: m.tip,
				height: newheight,
				miner:  mi,
				time:   g.currenttime,
			})
			if g.traceenable {
				fmt.Printf("%.3f %s mined-newid %d on %d height %d\n",
					g.currenttime, m.name, bid, m.tip, newheight)
			}
		} else {
			// Block received from a peer (but could be a stale message).
			if !validblock(bid) || getheight(bid) <= getheight(m.tip) {
				// We're already mining on a block that's at least as good.
				continue
			}
			// This block is better, switch to it, first compute reorg depth.
			if g.traceenable {
				fmt.Printf("%.3f %s received-switch-to %d\n",
					g.currenttime, m.name, bid)
			}
			reorg := reorgDepth(m.tip, bid)
			if reorg > 0 && g.traceenable {
				fmt.Printf("%.3f %s reorg %d maxreorg %d\n",
					g.currenttime, m.name, reorg, g.maxreorg)
			}
			if g.maxreorg < reorg {
				g.maxreorg = reorg
			}
		}
		relay(mi, bid)
		startMining(mi, bid)
	}
	cleanBlocks()

	base := g.blocks[0]
	bestchainblocks := base.height

	// The base block is mined and on the best chain, but it's the one block
	// cleanBlocks() never drops, so count it here. (Getting this wrong is
	// what used to make the stale count come out negative for short runs.)
	minedblocks := g.mined
	minedfinal := make([]height, len(g.miners))
	for i, m := range g.miners {
		minedfinal[i] = m.minedfinal
	}
	if bestchainblocks > 0 {
		minedblocks++
		minedfinal[base.miner]++
	}
	staleblocks := minedblocks - bestchainblocks

	fmt.Printf("%-20s %14d\n", "seed-arg", g.seed)
	fmt.Printf("%-20s %14d\n", "block-interval-arg", g.blockinterval)
	fmt.Printf("%-20s %14d\n", "stopheight-arg", g.stopheight)
	fmt.Printf("%-20s %14d\n", "total-hashrate-arg", g.totalhash)
	fmt.Printf("%-20s %14d\n", "mined-blocks", minedblocks)
	fmt.Printf("%-20s %14.3f\n", "total-simtime", base.time)
	fmt.Printf("%-20s %14.3f\n", "ave-block-time",
		float64(base.time)/float64(bestchainblocks))
	fmt.Printf("%-20s %14d\n", "stale-blocks", staleblocks)
	fmt.Printf("%-20s %14.2f%%\n", "stale-rate",
		float64(staleblocks*100)/float64(minedblocks))
	fmt.Printf("%-20s %14d\n", "max-reorg-depth", g.maxreorg)
	for i, m := range g.miners {
		fmt.Printf("miner %-13s  hashrate-arg %6d %6.2f%% ", m.name,
			m.hashrate, float64(m.hashrate*100)/float64(g.totalhash))
		fmt.Printf("blocks %6.2f%% ",
			float64(m.credit*100)/float64(bestchainblocks))
		fmt.Printf("stale-rate %6.2f%%",
			float64((minedfinal[i]-m.credit)*100)/float64(minedfinal[i]))
		fmt.Println("")
	}
}
