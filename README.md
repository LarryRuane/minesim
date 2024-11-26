# minesim -- Cryptocurrency POW mining simulator

This program simulates a POW mining network, such as Bitcoin or Zcash
(or many others). 

## License

This software is released under the terms of the MIT license,
see https://opensource.org/licenses/MIT.

## Acknowledgment

This work has been supported by a Brink grant (https://brink.dev/).

## Introduction

[TABConf2021 presentation slides](https://docs.google.com/presentation/d/e/2PACX-1vTvKsjLbYmbURJUkPiXq-u4rrSGcby6cdDGDxPGDILixRptlHcrqBWbHtSadtxjr-ki2sCgxkrLnf_N/pub)

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
- chain splits (reorgs, stale blocks)

It does not simulate:

- actual POW computation (hashing)
- transactions
- real block forwarding (https://developer.bitcoin.org/devguide/p2p_network.html#block-broadcasting)
- Byzantine (faulty or malicious) behaviors
- hard or soft forks (changing the validity rules)
- initial block download (IBD, initial sync)
- non-mining nodes
- miners arriving and leaving
- difficulty adjustment
- variable block rewards over time (4-year halvings)
- network message loss, network partitions, sybil or eclipse attacks
- randomly-varying message latencies (this wouldn't be hard to do)
- mining pools (although the "miner" entities here could be considered to be pools)

## Configuration file

The configuration file consists of one line per miner with
whitespace-separated tokens, specifying

- miner identifier (string)
- its hashrate (floating point)
- a list of peers, each is a pair of:
  - miner id (string)
  - relay latency (floating point)

Empty lines and lines beginning with `# ` are ignored.

The miners' hashrate has arbitrary units; what matters is the value of
each to the total network hashrate. In other words, you could scale
all hashrates by a constant factor and the simulation wouldn't change.

Peer specifications are one-way: If miner A lists miner B as a peer,
A sends to B but that doesn't allow B to send to A;
that must be specified explicitly.

Each peer must have at least one *inbound* connection (another peer
listing a connection to it), otherwise it won't receive any blocks and
will mine on it's own chain for the entire run.
This behaviour will manifest itself by returning `NaN` for the "blocks"
and "stale" fields in the results table.

## Building, running, and startup

To build: `go build minesim.go`

To run: "`./minesim` _options_" or "`go run minesim.go` _options_"

Available options (`./minesim -help`):
- `-f` (string) File -- network configuration, default `./network`
- `-t` (boolean) Tracing -- shows each execution step as a line to standard output
- `-i` (integer32) Interval -- the average block interval, units are arbitrary but usually interpreted as seconds
- `-h` (integer64) Height -- stop simulation at this height
- `-s` (integer64) Seed -- for the random number generator; default is 0; specify -1 to use wall-clock time

## Block relay

The only type of message that peers send to each other is
block-forwarding. These happen automatically; there are no request messages.
There are two sources of knowledge of a new block
(which are always immediately forwarded to all known peers): blocks that
the peer received from another peer, and blocks that the sending peer
mined itself. Miners always act honestly.

The miners begin mining on the "genesis" block, which has height
zero. When a miner solves a block, it relays it to its peers by "sending"
a message with the configured peer latency. When a miner hears about a
block from a peer, it checks whether the block it's currently mining
on has the same or higher height; if so, it ignores the received block
(does not forward it, because it's already forwarded the as-good or better block
that it's mining on). If, on the other hand, the received block is better
than the one it's working on, it switches to it, that is, starts mining on top
of it. It also immediately relays this block to its peers.

Block verification is not modeled explicitly, but can be considered as part
of the block relay latency. (If you want to model blocks that take a long
time to verify, you may want to increase the peer latencies.)

Peer connections are one-way; if you want two peers to be able to forward
blocks to each other, each must list the other as a peer. The latency
in each direction can be different. The network file in this
repository shows an example of a configuration that models two groups of
closely-connected miners, one group in China and the other in Iceland. The
latency within the groups is low, but between groups is high.

The `-i` interval argument simulates the given block time, but it may end
up greater because of losses due to chain splits (mined blocks that end
up being stale blocks). The real Bitcoin network's difficulty adjustment
algorithm corrects for this, but this simulator doesn't include
difficulty adjustment.

## Default configuration

The file `network` (included in the repo) has the default configuration:

```
# two groups of miners far apart (the times must include block verification)
china-asic     500    china-gateway 0.5   china-gpu  0.2
china-gpu       80    china-gateway 0.5   china-asic 0.2
portable        60    china-gateway 0.5
china-gateway   20    china-asic 0.5      china-gpu  0.5    iceland-gw 12   portable 0.5

iceland-gw     500    china-gateway 15    iceland2 0.5
iceland2       600    iceland-gw 0.5
```

This is a trivial, slightly realistic configuration, but illustrates the
concepts. Each line described a miner (empty lines or those beginning with
`# ` are ignored) and consists of whitespace-separated tokens. The first
token is the name of the miner, the second is its relative hashrate. The
remainder of the line consists of pairs of peer and latency to send
to that peer. Again, the time units are arbitrary, but seconds works
well. Two-way peer communication paths must be configured explicitly.

This configuration imagines mining centers in China and Iceland,
and the block relay times are much less within each center than between
centers. This is highly arbitrary and made-up; it would be interesting to
create a configuration file that matches the real network. (The simulator
scales well; it's reasonable to configure many thousands of miners.)

## Results

At the end of the run, the simulator shows various statistics, for example,
with the default network configuration:

```
$ go run minesim.go
seed-arg                          0
block-interval-arg              600
stopheight-arg              1000000
total-hashrate-arg             1760
mined-blocks                1011317
total-simtime         607746829.335
ave-block-time              607.747
stale-blocks                  11318
stale-rate                     1.12%
max-reorg-depth                   3
miner china-asic     hashrate-arg    500  28.41% blocks  28.19% stale-rate   1.81%
miner china-gpu      hashrate-arg     80   4.55% blocks   4.55% stale-rate   1.89%
miner portable       hashrate-arg     60   3.41% blocks   3.38% stale-rate   1.90%
miner china-gateway  hashrate-arg     20   1.14% blocks   1.13% stale-rate   1.78%
miner iceland-gw     hashrate-arg    500  28.41% blocks  28.46% stale-rate   0.69%
miner iceland2       hashrate-arg    600  34.09% blocks  34.28% stale-rate   0.70%
```

(The `*-arg` values are arguments to the simulation, not computed
values.) The per-miner output shows the hashrate argument (just
repeating what's in the network configuration file), the percentage
of best-chain blocks that this miner earned, and the stale block
rate, which is the percentage of blocks mined _by this miner_ that
did not end up in the best chain. (This is not the fraction of 
overall stale blocks generated by this miner.)

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
0.000 china-asic start-on 1000 height 0 mined 0 credit 0 solve 1959.96
0.000 china-gpu start-on 1000 height 0 mined 0 credit 0 solve 37249.50
0.000 portable start-on 1000 height 0 mined 0 credit 0 solve 19224.70
0.000 china-gateway start-on 1000 height 0 mined 0 credit 0 solve 30399.34
0.000 iceland-gw start-on 1000 height 0 mined 0 credit 0 solve 1167.42
0.000 iceland2 start-on 1000 height 0 mined 0 credit 0 solve 2043.34
1167.419 iceland-gw mined-newid 1001 on 1000 height 1
1167.419 iceland-gw start-on 1001 height 1 mined 1 credit 0 solve 143.38
1167.919 iceland2 received-switch-to 1001
1167.919 iceland2 start-on 1001 height 1 mined 0 credit 0 solve 299.58
1182.419 china-gateway received-switch-to 1001
1182.419 china-gateway start-on 1001 height 1 mined 0 credit 0 solve 5385.55
1182.919 china-asic received-switch-to 1001
1182.919 china-asic start-on 1001 height 1 mined 0 credit 0 solve 756.05
1182.919 china-gpu received-switch-to 1001
1182.919 china-gpu start-on 1001 height 1 mined 0 credit 0 solve 9557.39
1182.919 portable received-switch-to 1001
1182.919 portable start-on 1001 height 1 mined 0 credit 0 solve 29569.32
1310.803 iceland-gw mined-newid 1002 on 1001 height 2
1310.803 iceland-gw start-on 1002 height 2 mined 2 credit 0 solve 509.28
```

The first column is the simulated time. At time zero, all six miners
start mining on top of block ID 1000 (which is the arbitrary ID of the
"genesis block") which has height 0. So far, each miner has mined zero
blocks and received credit for zero blocks. (A credit is received when
it's certain that a block will be part of the final blockchain.) The
number following `solve` is the time it will take for this miner to
solve the next block (according to the random Poisson distribution).

At time 1167.419, the miner `iceland-gw` mines the first block which
gets the next ID (1001, the IDs just increment on each mined block;
IDs are globally unique). `iceland-gw` begins mining on top of that block
(`start-on 1001`). It also relays the new block to its peer miners.
Its peer `iceland2` receives the block quickly and begins to mine
on top of it. After about 15 seconds, `china-gateway` receives the
block, starts mining on top of it, and relays it to its peers.

Searching this trace output for `reorg` is interesting; this shows
cases where a miner discovers that a better block that requires it to
back up _more_ than one block to get on the best chain. As expected,
as block relay times increase or the average block interval decreases
in the configuration, deeper reorgs occur.

## Block-interval sample time generators

This repository also includes two simple Python programs to generate
simulated block intervals based on the Poisson distribution. They have
equivalent functionality, but `blockint.py` is much more efficient. Its
algorithm is used in the simulator. The other program, `blockint-count.py`,
more closely simulates mining by repeatedly attempting to "solve" a block,
not by hashing but by generating random numbers.

## Future improvements

- Variable (random) message delays
- Unreliable network (random dropped messages)
- Automatic node creation and peer connection, not just a static network
- Nodes dynamically joining and leaving the network
- Dynamic network connections (network partitions and healing)
- Difficulty adjustment
- Forks (hard and soft), chain wipeout
- Nonstandard behaviors such as selfish mining

## Exercises, discussion questions

- Run minsim with the default configuration (`go run minesim`)
- Do miners earn blocks in proportion to their hashrates?
- If not, are the differences due to chance?
  Are the results different if you run with:
  - Longer simulation (for example, `-h 10000000`)
  - Different seeds (for example, random seed `-s -1`)
- _Note_ The default configuration (the `network` file) sets up two geographic
  mining areas, China and Iceland. Each area has some miners (or pools),
  with the network latency (block propagation delay) being small within
  each area, and much larger latencies between areas.
- See what happens when you change the configuration (`network` file) so that:
  - All latencies are zero (infinitely fast network)
  - Latencies between geographic areas increases or decreases
    - Is lunar mining practical? Martian mining?
  - Latencies between geographic areas are larger than the block time
  - A miner is completely isolated from the rest of the network
    - Why these unexpected results? (Requires some understanding of the simulator implementation)
- For each of the variations listed above:
  - What happens to the overall stale block rate?
  - How does each miner's blocks won differ from its hashrate?
  - What happens to the maximum reorg depth seen during the simulation run?
- What are the effects of changing the block interval from the default 600
  seconds (10 minutes)?
  - Based on these results, what's your opinion of shortening the block interval
  (sometimes suggested as a way to make confirmations faster)?
- The default configuration file (`network`) specifies a miner called `portable` in China
  - What happens to its efficiency (number of stale blocks and percentage of blocks won)
  if you move this miner from China to Iceland?
  - If there are differences, how do you interpret them?
  - Is the advantage of moving from China to Iceland affected by the block interval?
    - If so, what's the effect of block interval on geographic centralization?
- Run the simulator with tracing enabled
  (`-t`, you'll want to pipe its output to a program like `less`)
  - _Note_ Remember that each block has a unique identifier (its block id), and these
  begin at 1000 (the genesis block); more than one block (id) can have the same height
  - Which miner mines the first block (block id 1001)?
  - How do the other miners react to receiving this block?
  - When does the first reorg happen? (_hint_ pipe to `less` and search for "reorg")
    - Briefly explain the sequence of events that caused the reorg
    - Are reorgs a network-wide phenomenon or a node-specific phenomenon?
    - Which kinds of miners are more likely to experience reorg events?
- Auto-generate a network configuration file with hundreds or thousands of miners,
  see what happens and whether the simulator scales reasonably.
- Does this simulator leave important things out of its model?
- _Advanced exercises:_
  - Modify the simulator to model the
[selfish mining attack](https://www.cs.cornell.edu/~ie53/publications/btcProcFC.pdf).
    - Run the simulator with various network configurations, do you observe the
    effects of the attack?
    - Modify the simulator to implement the mitigation proposed by that paper.
    - Run the simulator, does the mitigation help?
  - Enhance the simulator to add configurable random "jitter"
    to the block relay latencies to more closely simulate real networks,
    see what the effects are.
