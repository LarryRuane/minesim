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
- random block discovery (mining) according to (realistic) poisson distribution
- a configurable set of peer mining nodes, each with a specified hash power
- configurable peer network topology
- simple block forwarding (relaying to peers)
- message-passing latency from each peer to its given set of other peers
- chain splits (reorgs)

It does not simulate:

- actual POW computation (hashing)
- transactions
- real block forwarding (https://bitcoin.org/en/p2p-network-guide#block-broadcasting)
- Byzantine (faulty or malicious) behaviors
- non-mining nodes
- miners coming and going
- difficulty adjustment
- variable block rewards over time (4-year halvings)
- network message loss, network partitions
- randomly-varying message latencies (this wouldn't be hard to do)

### Configuration file

The configuration file consists of one line per miner with
whitespace-separated tokens, specifying

- miner identifier (string)
- its hashrate (floating point)
- a list of peers, each is a pair of: miner id (string), relay delay (floating point)

Empty lines and lines beginning with # are ignored.

The miners' hash rate has arbitrary units; what matters is the value of
each to the total network hash rate.

The configuration pathname is a required argument; it can be preceded by the following options:

- `-t` (boolean) Tracing -- shows each execution step as a line to standard output
- `-r` (integer) Repetitions -- the number of simulation execution steps
- `-i` (floating point) Interval -- the average block interval, units are arbitrary but usually interpreted as seconds
- `-d` (floating point) Difficulty -- default is 1.0, higher will increase the average block interval
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
up being orphaned). The `-d` (difficulty) argument does not fix the block
interval, but lets it vary according to hash rate. It's probably more
natural to use the interval argument, in which case the per-miner hash
rates are only meaningful as their fraction of the total hash rate. (That
is, you could multiply all the hash rates by the same factor and the
simulation would be unchanged.)

### Results

At the end of the run, the simulator shows various statistics, for example:

```
$ go run minesim.go -i 75 -r 1000000 network
seed-arg 0
block-interval-arg 75.00
mined-blocks 77822
height 76937 98.86%
total-simtime 5837580.96
ave-block-time 75.87
total-hashrate 3450.00
total-orphans 885
baseblockid 77823
miner china-asic hashrate-arg 500.00 14.49% blocks 14.15% orphans 4.65%
miner china-gpu hashrate-arg 80.00 2.32% blocks 2.21% orphans 4.87%
miner china-gateway hashrate-arg 20.00 0.58% blocks 0.54% orphans 2.79%
miner iceland-gw hashrate-arg 800.00 23.19% blocks 23.36% orphans 0.53%
miner iceland2 hashrate-arg 2050.00 59.42% blocks 59.73% orphans 0.34%
```

(The `*-arg` values are arguments to the simulation, not computed
values.) The simulation created a blockchain with 76937 blocks (height),
which was 98.86% of all blocks mined (77822). The difference is the number
of orphan blocks (885). The miner `china-gpu` had the highest orphan rate:
4.87% of the blocks it mined ended up being reorged away. Hash rates
and network topology affect these orphan rates.

The average block time was 75.87 seconds, which differs from the requested
75 seconds due to random variations, and also due to orphaned blocks --
the higher the orphan rate, the greater the average block time will be
because some of the hash power is wasted.

### Block-interval simulators

This repository also includes two simple Python programs to generate
simulated block intervals based on the Poisson distribution. They have
equivalent functionality, but `blockint.py` is much more efficient. Its
algorithm is used in the simulator. The other program more closely
simulates actual mining (repeated attempts to "solve" a block).
