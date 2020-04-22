# minesim -- Cryptocurrency POW mining simulator

This program simulates a POW mining network, such as Zcash or Bitcoin
(or many others). 

## License

This software is released under the terms of the MIT license, see https://opensource.org/licenses/MIT.

### Introduction

Like any simulator, it abstracts away a huge amount of
stuff (if it didn't, it wouldn't be a simulator, it would be the thing
itself). It's a single executable written in Go. It can simulate the
generation of many thousands of blocks per second of real CPU time. It
simulates:

- the passage of time; the units of time are arbitrary (but seconds seems to work well)
- random block discovery (mining) according to (realistic) Poisson distribution
- a configurable set of peer mining nodes, each with a specified hash power
- configurable peer network topology (not necessarily fully-connected)
- simple block forwarding (relaying to peers)
- message-passing latency from each peer to its peers
- chain splits (reorgs, stale or orphan blocks)

It does not simulate:

- actual POW computation (hashing)
- transactions
- real block forwarding (https://bitcoin.org/en/p2p-network-guide#block-broadcasting)
- Byzantine (faulty or malicious) behaviors
- non-mining nodes
- miners arriving and leaving
- difficulty adjustment
- variable block rewards over time (4-year halvings)
- network message loss, network partitions, sybil or eclipse attacks
- randomly-varying message latencies (this wouldn't be hard to do)

### Configuration file

The configuration file consists of one line per miner with
whitespace-separated tokens, specifying

- miner identifier (string)
- its hashrate (floating point)
- a list of peers, each is a pair of: miner id (string), relay delay (floating point)

Empty lines and lines beginning with # are ignored.

The miners' hashrate has arbitrary units; what matters is the value of
each to the total network hashrate.

Peer specifications are one-way: If miner A lists miner B as a peer,
A sends to B but that doesn't allow B to send to A;
that must be specified explicitly.

### Building, running, and startup

To build: `go build minesim.go`

To run: "`./minesim` _options_" or "`go run minesim.go` _options_

Available options (`./minesim -help`):
- `-f` (string) File -- network configuration, default `./network`
- `-t` (boolean) Tracing -- shows each execution step as a line to standard output
- `-r` (integer) Repetitions -- the number of simulation execution steps
- `-i` (floating point) Interval -- the average block interval, units are arbitrary but usually interpreted as seconds
- `-s` (integer) Seed -- for the random number generator; default is 0; specify -1 to use wall-clock time

### Block relay

The only type of message that peers send to each other is
block-forwarding. There are two sources of knowledge of a new block
(which are always immediately forwarded to all known peers): blocks that
the peer heard about from another peer, and blocks that the sending peer
mined itself.

The miners begin mining on the "genesis" block, which has height
zero. When a miner solves a block, it relays it to its peers by "sending"
a message with the configured peer delay. When a miner hears about a
block from a peer, it checks whether the block it's currently mining
on has the same or higher height; if so, it ignores the received block
(does not forward it, because it's already forwarded the better block
that it's mining on). If, on the other hand, the received block is better
than the one it's working on, it switches to it, starts mining on top
of it. It also relays this block to its peers.

Peer connections are one-way; if you want two peers to be able to forward
blocks to each other, each must list the other as a peer. The latency
(delay) in each direction can be different. The network file in this
repository shows an example of a configuration that models two groups of
closely-connected miners, one group in China and the other in Iceland. The
latency within the groups is low, but between groups is high.

The `-i` interval argument simulates the given block time, but it may end
up greater because of losses due to chain splits (mined blocks that end
up being orphaned).

### Results

At the end of the run, the simulator shows various statistics, for example:

```
$ go run minesim.go -i 75 -r 1000000
seed-arg 0
block-interval-arg 75.00
mined-blocks 112441
height 111139 98.84%
total-simtime 8453695.57
ave-block-time 76.06
total-hashrate-arg 3450.00
total-stale 1302
baseblockid 112434
repetitions-arg 1000000
repetitions 1000004
miner china-asic hashrate-arg 500.00 14.49% blocks 14.03% stale 4.77%
miner china-gpu hashrate-arg 80.00 2.32% blocks 2.19% stale 4.99%
miner china-gateway hashrate-arg 20.00 0.58% blocks 0.55% stale 3.48%
miner iceland-gw hashrate-arg 800.00 23.19% blocks 23.33% stale 0.56%
miner iceland2 hashrate-arg 2050.00 59.42% blocks 59.89% stale 0.34%
```

(The `*-arg` values are arguments to the simulation, not computed
values.) The simulation created a blockchain with 111139 blocks (height),
which was 98.84% of all blocks mined (112441). The difference is the
number of stale (orphan) blocks (1302). The miner `china-gpu` had
the highest orphan rate: 4.99% of the blocks it mined ended up being
reorged away. Individual miner hashrate and network topology affect
these orphan rates.

The average block time was 76.06 seconds, which, in the simulator,
is greater than the requested block interval due to orphaned blocks.
The higher the orphan rate, the greater the average block time because
some of the hash power is wasted. (The time can differ from expected
also due to random variations.) In the real world, difficulty adjustment
keeps the block interval close to the desired value. (This simulator
doesn't include difficulty adjustment.)

Miner `china-gpu` was configured with the lowest hash power, 2.32%, and
mined only 2.19% of the blocks, which is about 6% less than its hashrate
fraction. It had the highest fraction of blocks it mined turning out
to be stale, 4.99%. It was at a disadvange because its hashrate was low
and it's "far" from most of the mining power in Iceland, especially due
to its network block relay messages having to go through `china-gateway`.

Miner `iceland2` did the best, having achieved superlinear rewards,
getting credit for 59.89% of blocks with "only" 59.42% of the
hashrate. That's because of its high hashrate and its close proximity
to another strong miner, `iceland-gw`.

### Block-interval simulators

This repository also includes two simple Python programs to generate
simulated block intervals based on the Poisson distribution. They have
equivalent functionality, but `blockint.py` is much more efficient. Its
algorithm is used in the simulator. The other program more closely
simulates actual mining (repeated attempts to "solve" a block).
