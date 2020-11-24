# minesim -- Cryptocurrency POW mining simulator

This program simulates a POW mining network, such as Zcash or Bitcoin
(or many others). 

## License

This software is released under the terms of the MIT license,
see https://opensource.org/licenses/MIT.

## Introduction

Like any simulator, this one abstracts away a huge amount of stuff (if it
didn't, it wouldn't be a simulator, it would be the thing itself). It's
a single executable written in Go. It can simulate the generation of
many thousands of blocks per second of real CPU time.

The purpose of this simulator is to investigate how block relay delays
(network messages and block verification) can cause miners to not be
mining on the best chain, as discussed here:

- https://podcast.chaincode.com/2020/03/12/matt-corallo-6.html
- https://youtu.be/RguZ0_nmSPw?t=752

It simulates:

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

## Configuration file

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

## Building, running, and startup

To build: `go build minesim.go`

To run: "`./minesim` _options_" or "`go run minesim.go` _options_

Available options (`./minesim -help`):
- `-f` (string) File -- network configuration, default `./network`
- `-t` (boolean) Tracing -- shows each execution step as a line to standard output
- `-r` (integer) Repetitions -- the number of simulation execution steps
- `-i` (floating point) Interval -- the average block interval, units are arbitrary but usually interpreted as seconds
- `-s` (integer) Seed -- for the random number generator; default is 0; specify -1 to use wall-clock time

## Block relay

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

## Default configuration

The file `network` (included in the repo) has the default configuration:

```
$ cat network 
# two groups of miners far apart (the times must include block verification)
china-asic     500    china-gateway 0.5
china-gpu       80    china-gateway 0.5
china-gateway   20    china-asic 0.5      china-gpu 0.5  iceland-gw 2

iceland-gw     800    china-gateway 2     iceland2 0.5
iceland2      2050    iceland-gw 0.5
$
```

This is a trivial and unrealistic configuration, but illustrates the
concepts. Each line described a miner (empty lines or those beginning with
`#` are ignored) and consists of whitespace-separated tokens. The first
token is the name of the miner, the second is its relative hashrate. The
remainder of the line consists of pairs of peer and latency to send
to that peer. Again, the time units are arbitrary, but seconds works
well. Two-way peer communication paths must be configured explicitly.

This configuration imagines a mining centers in China and Iceland,
and the block relay times are much less within each center than between
centers. This is highly arbitrary and made-up; it would be interesting to
create a configuration file that matches the real network. (The simulator
can scale well; it's reasonable to configure many thousands of miners.)

## Results

At the end of the run, the simulator shows various statistics, for example:

```
$ go run minesim.go -i 75 -r 1000000
seed-arg 0
block-interval-arg 75.00
mined-blocks 112441
height 111139 98.84%
total-simtime 8453680.15
ave-block-time 76.06
total-hashrate-arg 3450.00
total-stale 1303
max-reorg-depth 3
baseblockid 112433
repetitions-arg 1000000
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

## Motivations for writing this simulator

This simulator hopes to make cryptocurrency developers aware of the
dangers of reducing block interval or increasing block size. Doing
either of these increases the "gravitational pull" that miners and
pools experience to be near other centers of high hashrate. In physics,
gravity is a weak force -- many other forces can overcome it -- but
it is a force. Mining is an extremely competitive industry with low
barriers to entry and razor-thin profit margins, so if a miner or a
mining pool can physically locate near other miners, its 2 percent
profit may double! Geographic centralization makes attacking the
network easier by, for example, governments, who can shut down a
large fraction of mining power that is within their jurisdiction.

## Trace output

Specifying `-t` (enable tracing) on the command line causes the simulator
to print a line for each execution step, and it's fun to see these
details. Here's the beginning of the output using the default arguments:


```
$ go run minesim.go -t
0.000 china-asic start-on 1000 height 0 mined 0 credit 0 solve 1920.98
0.000 china-gpu start-on 1000 height 0 mined 0 credit 0 solve 36508.74
0.000 china-gateway start-on 1000 height 0 mined 0 credit 0 solve 56527.16
0.000 iceland-gw start-on 1000 height 0 mined 0 credit 0 solve 744.87
0.000 iceland2 start-on 1000 height 0 mined 0 credit 0 solve 279.07
279.074 iceland2 mined-newid 1001 on 1000 height 1
279.074 iceland2 start-on 1001 height 1 mined 1 credit 0 solve 586.16
279.574 iceland-gw received-switch-to 1001
279.574 iceland-gw start-on 1001 height 1 mined 0 credit 0 solve 87.83
281.574 china-gateway received-switch-to 1001
281.574 china-gateway start-on 1001 height 1 mined 0 credit 0 solve 8808.79
282.074 china-asic received-switch-to 1001
282.074 china-asic start-on 1001 height 1 mined 0 credit 0 solve 211.14
282.074 china-gpu received-switch-to 1001
282.074 china-gpu start-on 1001 height 1 mined 0 credit 0 solve 4631.35
367.407 iceland-gw mined-newid 1002 on 1001 height 2
367.407 iceland-gw start-on 1002 height 2 mined 1 credit 0 solve 936.73
367.907 iceland2 received-switch-to 1002
367.907 iceland2 start-on 1002 height 2 mined 1 credit 1 solve 848.23
...
```

The first column is the simulated time. At time zero, all five miners
start mining on top of block ID 1000 (which is the arbitrary ID of the
"genesis block") which has height 0. So far, each miner has mined zero
blocks and received credit for zero blocks. (A credit is received when
it's certain that a block will be part of the final blockchain.) The
number following `solve` is the time it will take for this miner to
solve the next block (according to the random Poisson distribution).

At time 279.074, the miner `iceland2` solves the first block which
gets the next ID (1001, the IDs just increment on each mined block;
IDs are globally unique). `iceland2` begins mining on top of that block
(`start-on 1001`). It also relays the new block to the other miners.
Its only peer is `iceland-gw`, which receives the block at 279.574
(500 milliseconds later). This miner determines that this new block
is better than the one it's currently mining on so it switches to it
(`received-switch-to 1001`) and begins mining on it (`start-on 1001`). It
also forwards the block to its peers; this is how `china-gw` receives
it at 281.574 (2 seconds later), and it switches to it and relays it.

Searching this trace output for `reorg` is interesting; this shows
cases where a miner discovers that a better block that requires it to
back up _more_ than one block to get on the best chain. As expected,
as block relay times increase or the average block interval decreases
in the configuration, deeper reorgs occur.

## Block-interval sample time generators

This repository also includes two simple Python programs to generate
simulated block intervals based on the Poisson distribution. They have
equivalent functionality, but `blockint.py` is much more efficient. Its
algorithm is used in the simulator. The other program more closely
simulates actual mining (repeated attempts to "solve" a block).
