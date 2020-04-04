// This program simulates a network of block miners in a proof of work system. You specify
// a network topology, and a hash rate for each miner. The time units are whatever you'd
// like them to be. Here's an example input file:

package main

import (
	"bufio"
	"container/heap"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
)

var (
	currenttime float64
	r           = rand.New(rand.NewSource(99))
	blocks      = make([]block_t, 1)     // ordered by oldest first
	baseblockid int64                    // blocks[0] corresponds to this block id
	tips        = make(map[int64]int, 0) // for pruning
	bestblock   int64                    // tip has a max height (may not be the only one)
	miners      []miner_t                // one per miner (unordered)
	eventlist   = make(eventlist_t, 0)   // priority queue, lowest timestamp first
)

type block_t struct {
	parent  int64 // first block is the only block with parent = zero
	height  int   // more than one block can have the same height
	miner *miner_t   // which miner found this block
}

func getblock(blockid int64) *block_t {
	return &blocks[blockid-baseblockid]
}
func getheight(blockid int64) int {
	return blocks[blockid-baseblockid].height
}

// The set of miners is static (at least for now)
type (
	peer_t struct {
		miner *miner_t
		delay   float64
	}
	miner_t struct {
		hashrate int      // how much hashing power this miner has
		mined    int      // how many blocks we've mined total (including reorg)
		credit   int      // how many blocks we've mined we get credit for
		peer     []peer_t // outbound peers (we forward blocks to these miners)
		current  int64    // the block we're trying to mine on top of (initially 0)
	}
)

// The only event is the arrival of a block at a miner; if the block id is zero,
// that means this miner mined this block.
type (
	event_t struct {
		when    float64 // when the block arrives
		to      *miner_t     // which miner gets the block
		blockid int64   // >0: block arriving on p2p, <0: block we're mining on
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

func (m *miner_t) stopMining() {
	tips[m.current]--
	if tips[m.current] == 0 {
		delete(tips, m.current)
	}
}

// Start mining on top of the given existing block
func (m *miner_t) startMining(blockid int64) {
	// We'll mine on top of blockid
	m.current = blockid
	tips[m.current]++

	// Relay our most recent block to our peers.
	for _, p := range m.peer {
		// add some jitter to this delay?
		heap.Push(&eventlist, event_t{
			when:    currenttime + p.delay,
			to:      p.miner,
			blockid: m.current})
	}

	// Schedule an event for when our "mining" will be done
	// (the larger the hashrate, the smaller the delay).
	delay := -math.Log(1.0 - r.Float64())
	delay *= float64(1e6) / float64(m.hashrate)
	// negative blockid means mining (not p2p)
	heap.Push(&eventlist, event_t{
		when:    currenttime + delay,
		to:      m,
		blockid: -blockid})
}

func main() {
	if len(os.Args) != 3 {
		fmt.Println("usage:", os.Args[0], "iterations network-file")
		os.Exit(1)
	}
	iterations, err := strconv.ParseInt(os.Args[1], 10, 32)
	if err != nil {
		fmt.Println("bad iterations:", err)
		os.Exit(1)
	}
	f, err := os.Open(os.Args[2])
	if err != nil {
		fmt.Println("open failed:", err)
		os.Exit(1)
	}
	minerMap := make(map[string][]string, 0)
	minerIndex := make(map[string]int, 0)
	i := 0
	scan := bufio.NewScanner(f)
	for scan.Scan() { // each line
		// Each line is a hashrate, then a list of pairs of
		// client id and delay (time to send to that client)
		line := scan.Text()
		if len(line) == 0 {
			continue
		}
		if line[0:1] == "#" {
			continue
		}
		fields := strings.Fields(line)
		if _, ok := minerMap[fields[0]]; ok {
			fmt.Println("duplicate miner name:", fields[0])
			os.Exit(1)
		}
		minerMap[fields[0]] = fields[1:]
		minerIndex[fields[0]] = i
		i++
	}
	miners = make([]miner_t, i)
	for k, v := range minerMap {
		// v is a slice of whitespace-separated tokens (on a line)
		hr, err := strconv.ParseInt(v[0], 10, 32)
		if err != nil {
			fmt.Println("bad hashrate:", v[0], err)
			os.Exit(1)
		}
		m := miner_t{hashrate: int(hr)}
		v = v[1:]
		if (len(v) % 2) > 0 {
			fmt.Println("bad client delay pairs:", k, v)
			os.Exit(1)
		}
		for len(v) > 0 {
			minerid := minerIndex[v[0]]
			delay, err := strconv.ParseFloat(v[1], 64)
			if err != nil {
				fmt.Println("bad delay:", v[1], err)
				os.Exit(1)
			}
			m.peer = append(m.peer, peer_t{&miners[minerid], delay})
			v = v[2:]
		}
		miners[minerIndex[k]] = m
	}

	// Start all miners off mining their first blocks.
	heap.Init(&eventlist)
	for _, m := range miners {
		// begin mining on block "zero"
		m.startMining(0)
	}

	// should this be based instead on time?
	for i := int64(0); i < iterations; i++ {
		// clean up unneeded blocks
		if len(tips) == 1 /*&& len(blocks) > 1*/ {
			for {
				if _, ok := tips[baseblockid]; ok {
					break
				}
				baseblockid++
				blocks = blocks[1:]
			}
		}
		event := heap.Pop(&eventlist).(event_t)
		currenttime = event.when
		m := event.to
		if event.blockid > 0 {
			// this block is from a peer, see if it's useful
			if event.blockid >= baseblockid &&
				getheight(m.current) < getheight(event.blockid) {
				// incoming block is better, switch to it
				m.stopMining()
				m.startMining(event.blockid)
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
		blocks = append(blocks, block_t{
			parent:  m.current,
			height:  getheight(m.current) + 1,
			miner: m})
		prev := m.current
		m.startMining(baseblockid + int64(len(blocks)) - 1)
		if bestblock == 0 || prev == bestblock {
			// we're extending what's already the best chain, easy
			bestblock = m.current
			m.credit++
			continue
		}
		if getheight(m.current) <= getheight(bestblock) {
			continue
		}
		// The current chain now has one more block than what was
		// the best chain, reorg, credits must be adjusted.
		m.credit++
		dec := bestblock // decrement credits on this branch
		inc := prev      // increment credits on this branch
		for dec != inc {
			db := getblock(dec)
			ib := getblock(inc)
			db.miner.credit--
			ib.miner.credit++
			dec = db.parent
			inc = ib.parent
		}
		bestblock = m.current
	}
	totalblocks := 0
	totalorphans := 0
	totalhash := 0
	for _, m := range miners {
		totalblocks += m.credit
		totalorphans += m.mined - m.credit
		totalhash += m.hashrate
	}
	fmt.Printf("total-blocks %d\n", totalblocks)
	fmt.Printf("total-simtime %d\n", int64(currenttime))
	fmt.Printf("ave-block-time %.2f\n", float64(currenttime)/float64(totalblocks))
	fmt.Printf("total-hash-rate %d\n", totalhash)
	fmt.Printf("effective-hash-rate %.2f\n", 1e6/(float64(currenttime)/float64(totalblocks)))
	for k := range minerMap {
		m := &miners[minerIndex[k]]
		fmt.Printf("miner %s hashrate %d %.2f%% ", k, m.hashrate, float64(m.hashrate*100)/float64(totalhash))
		fmt.Printf("blocks %.2f%% ", float64(m.credit*100)/float64(totalblocks))
		fmt.Printf("orphans %.2f%%", float64((m.mined-m.credit)*100)/float64(totalorphans))
		fmt.Println("")
	}
}
