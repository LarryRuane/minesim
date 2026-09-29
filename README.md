# minesim -- Cryptocurrency POW mining simulator

This program simulates a POW mining network, such as Bitcoin or Zcash
(or many others). 

## License

This software is released under the terms of the MIT license,
see [LICENSE](LICENSE).

## Acknowledgment

This work has been supported by a Brink grant (https://brink.dev/).

## Introduction

[TABConf2021 presentation slides](https://docs.google.com/presentation/d/e/2PACX-1vTvKsjLbYmbURJUkPiXq-u4rrSGcby6cdDGDxPGDILixRptlHcrqBWbHtSadtxjr-ki2sCgxkrLnf_N/pub)

Like any simulator, this one abstracts away a huge amount of stuff (if it
didn't, it wouldn't be a simulator, it would be the thing itself). It can
simulate the generation of many thousands of blocks per second of real
CPU time.

There are two implementations, which take the same arguments, read the
same configuration files, and print the same statistics:

- `minesim.py`, a single Python script needing nothing but Python 3.10 or
  later -- no build step and nothing to install. Start here.
- `minesim.go`, a single Go program, about ten times faster, useful if you
  want to simulate millions of blocks.

The Python version also has some extra output options (`--format`,
`--chart`, `--dot`, `-n`) described below.

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
- its hashrate (whole number, greater than zero)
- a list of peers, each is a pair of:
  - miner id (string)
  - relay latency (floating point)

Empty lines, and lines whose first token begins with `#`, are ignored.

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

Because this is easy to do by accident and the `NaN`s don't explain
themselves, both programs check the configuration at startup and print a
warning to standard error:

```
$ ./minesim.py -f isolated-network
minesim: warning: miner lonely has no inbound peers, so it will never hear
about anyone else's blocks; it will mine its own private chain and the
statistics will come out as NaN
```

Such a miner also costs memory: the simulator can only discard blocks that
every miner agrees on, so a miner that never converges keeps the whole
chain alive. The `-p` (Go) or `--prune-depth` (Python) option bounds this;
see below.

## Building, running, and startup

The Python version needs no build step:

```
./minesim.py                 # or: python3 minesim.py
./minesim.py --help
```

To build and run the Go version: `go build minesim.go` then "`./minesim`
_options_", or just "`go run minesim.go` _options_".

Both accept the same options (note that `-h` means Height, not help; use
`--help` for help):
- `-f` (string) File -- network configuration, default `./network`
- `-t` (boolean) Tracing -- shows each execution step as a line to standard output
- `-i` (integer) Interval -- the average block interval, units are arbitrary but usually interpreted as seconds
- `-h` (integer) Height -- stop simulation at this height
- `-s` (integer) Seed -- for the random number generator; default is 0; specify -1 to use wall-clock time
- `-p` / `--prune-depth` (integer) -- bound memory if the miners' chains
  never converge (see the note on isolated miners above); 0 means no
  limit, default 100000. On a healthy network this never comes into play
  and has no effect on the results.

The Python version has these as well:
- `-n` (integer) -- repeat the run with N successive seeds and report the
  mean and standard deviation
- `--format` `text`, `json`, or `csv` -- how to print the results
- `--chart` -- add a bar chart of hashrate share against blocks won
- `--dot` -- draw the block tree with Graphviz instead of reporting statistics

A run of the default configuration to the default height of one million
blocks takes about 2 seconds with the Go version and about 27 seconds with
the Python one. Both scale to thousands of miners, but the cost grows with
the number of peer connections, since every miner relays every new block to
all of its peers.

The two programs will not produce the same numbers for a given `-s` seed,
because Go and Python have different random number generators. Each is
reproducible on its own: the same seed always gives the same run.

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
$ ./minesim.py
seed-arg                          0
block-interval-arg              600
stopheight-arg              1000000
total-hashrate-arg             1760
mined-blocks                1011383
total-simtime         607592272.190
ave-block-time              607.593
stale-blocks                  11384
stale-rate                     1.13%
max-reorg-depth                   3
miner china-asic     hashrate-arg    500  28.41% blocks  28.21% stale-rate   1.82%
miner china-gpu      hashrate-arg     80   4.55% blocks   4.49% stale-rate   1.96%
miner portable       hashrate-arg     60   3.41% blocks   3.37% stale-rate   1.94%
miner china-gateway  hashrate-arg     20   1.14% blocks   1.13% stale-rate   1.78%
miner iceland-gw     hashrate-arg    500  28.41% blocks  28.54% stale-rate   0.66%
miner iceland2       hashrate-arg    600  34.09% blocks  34.26% stale-rate   0.72%
```

(The `*-arg` values are arguments to the simulation, not computed
values.) The per-miner output shows the hashrate argument (just
repeating what's in the network configuration file), the percentage
of best-chain blocks that this miner earned, and the stale block
rate, which is the percentage of blocks mined _by this miner_ that
did not end up in the best chain. (This is not the fraction of 
overall stale blocks generated by this miner.)

`go run minesim.go` prints the same table; its numbers differ slightly
because of the different random number generator.

### Other ways to see the results

`--chart` draws the comparison the simulator exists to make -- how much hash
power a miner has, against how many blocks it actually won:

```
$ ./minesim.py -h 30000 --chart
...
hashrate share (hash) vs best-chain blocks won (won)
china-asic    hash |############################      |  28.41%
              won  |############################      |  28.24%  -0.17
china-gpu     hash |####                              |   4.55%
              won  |####                              |   4.24%  -0.31
portable      hash |###                               |   3.41%
              won  |###                               |   3.46%  +0.05
china-gateway hash |#                                 |   1.14%
              won  |#                                 |   1.16%  +0.02
iceland-gw    hash |############################      |  28.41%
              won  |############################      |  28.49%  +0.09
iceland2      hash |##################################|  34.09%
              won  |##################################|  34.41%  +0.32
```

A single run can't tell you whether a gap like that is a real effect of the
network topology or just luck. `-n` runs the simulation several times with
different seeds and reports the spread, which can:

```
$ ./minesim.py -h 20000 -n 12
runs                             12
seed-args                     0..11
block-interval-arg              600
stopheight-arg                20000
total-hashrate-arg             1760
ave-block-time              604.671  +/- 3.221
stale-rate                    1.116% +/- 0.083
max-reorg-depth                   2  (over all runs)
miner china-asic     hashrate-arg    500  28.41% blocks  28.22% +/- 0.32 stale-rate   1.87% +/- 0.16
miner china-gpu      hashrate-arg     80   4.55% blocks   4.46% +/- 0.13 stale-rate   2.06% +/- 0.54
miner portable       hashrate-arg     60   3.41% blocks   3.34% +/- 0.13 stale-rate   2.03% +/- 0.65
miner china-gateway  hashrate-arg     20   1.14% blocks   1.11% +/- 0.04 stale-rate   1.72% +/- 1.14
miner iceland-gw     hashrate-arg    500  28.41% blocks  28.43% +/- 0.36 stale-rate   0.61% +/- 0.10
miner iceland2       hashrate-arg    600  34.09% blocks  34.45% +/- 0.25 stale-rate   0.68% +/- 0.09
```

Here the difference in *stale rate* between the Chinese miners (around 1.8
to 2.1 percent) and the Icelandic ones (around 0.6 percent) is several times
the spread, so it's real. Most of the individual gaps between hashrate share
and blocks won are not.

`--format json` and `--format csv` write the same numbers in a form you can
feed to another program, which is what you want for the parameter sweeps
suggested in the exercises below. CSV output is one row per miner per run,
with the run's arguments repeated on each row, so several runs concatenate
into one table:

```
$ ./minesim.py -h 20000 -n 20 --format csv > seeds.csv

$ for i in 30 60 120 300 600 1200; do            # sweep the block interval
      ./minesim.py -i $i -h 20000 --format csv | tail -n +2
  done > sweep.csv                               # (tail drops the repeated header)
```

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
$ ./minesim.py -t
0.000 china-asic start-on 1000 height 0 mined 0 credit 0 solve 3929.60
0.000 china-gpu start-on 1000 height 0 mined 0 credit 0 solve 18725.90
0.000 portable start-on 1000 height 0 mined 0 credit 0 solve 9604.55
0.000 china-gateway start-on 1000 height 0 mined 0 credit 0 solve 15821.11
0.000 iceland-gw start-on 1000 height 0 mined 0 credit 0 solve 1512.10
0.000 iceland2 start-on 1000 height 0 mined 0 credit 0 solve 913.59
913.586 iceland2 mined-newid 1001 on 1000 height 1
913.586 iceland2 start-on 1001 height 1 mined 1 credit 0 solve 2695.52
914.086 iceland-gw received-switch-to 1001
914.086 iceland-gw start-on 1001 height 1 mined 0 credit 0 solve 763.32
929.086 china-gateway received-switch-to 1001
929.086 china-gateway start-on 1001 height 1 mined 0 credit 0 solve 34182.90
929.586 china-asic received-switch-to 1001
929.586 china-asic start-on 1001 height 1 mined 0 credit 0 solve 1849.24
929.586 china-gpu received-switch-to 1001
929.586 china-gpu start-on 1001 height 1 mined 0 credit 0 solve 31510.97
929.586 portable received-switch-to 1001
929.586 portable start-on 1001 height 1 mined 0 credit 0 solve 12365.15
1677.403 iceland-gw mined-newid 1002 on 1001 height 2
1677.403 iceland-gw start-on 1002 height 2 mined 1 credit 0 solve 699.20
```

The first column is the simulated time. At time zero, all six miners
start mining on top of block ID 1000 (which is the arbitrary ID of the
"genesis block") which has height 0. So far, each miner has mined zero
blocks and received credit for zero blocks. (A credit is received when
it's certain that a block will be part of the final blockchain.) The
number following `solve` is the time it will take for this miner to
solve the next block (according to the random Poisson distribution).

At time 913.586, the miner `iceland2` mines the first block which
gets the next ID (1001, the IDs just increment on each mined block;
IDs are globally unique). `iceland2` begins mining on top of that block
(`start-on 1001`). It also relays the new block to its peer miners.
Its peer `iceland-gw` receives the block quickly and begins to mine
on top of it. After about 15 seconds, `china-gateway` receives the
block, starts mining on top of it, and relays it to its peers.

Searching this trace output for `reorg` is interesting; this shows
cases where a miner discovers that a better block that requires it to
back up _more_ than one block to get on the best chain. As expected,
as block relay times increase or the average block interval decreases
in the configuration, deeper reorgs occur.

## Drawing the block tree

Reading a fork out of the trace takes some effort. For a short run,
`--dot` writes the whole block tree in
[Graphviz](https://graphviz.org/) format instead of the statistics, with
the best chain in one color and stale blocks in another:

```
$ ./minesim.py -h 8 -s 21 --dot | dot -Tpng > tree.png
```

With that seed, blocks 1007 and 1008 are both mined on top of block 1006
before either miner hears about the other; 1008 wins and 1007 becomes a
stale block. Since this keeps every block ever mined in memory and draws
it, `--dot` only accepts small values of `-h`.

## Generating large configurations

Writing a configuration by hand stops being practical after a few dozen
miners, so `gennetwork.py` writes one for you. Miners are divided into
regions with low latency inside a region and high latency between regions
-- the same shape as the `network` file, just bigger:

```
$ ./gennetwork.py --region china:40 --region iceland:25 > big-network
$ ./minesim.py -f big-network -h 20000
```

Within a region the miners are joined into a ring (so the region is
certainly connected) and then given extra random peers; between regions,
only each region's gateway miners carry the traffic. Every connection is
written in both directions, so no miner ends up isolated. See
`./gennetwork.py --help` for the region sizes, latencies, peer counts,
hashrate range, and seed.

## Block-interval sample time generators

This repository also includes two simple Python programs to generate
simulated block intervals based on the Poisson distribution. They have
equivalent functionality, but `blockint.py` is much more efficient. Its
algorithm is used in the simulator. The other program, `blockint-count.py`,
more closely simulates mining by repeatedly attempting to "solve" a block,
not by hashing but by generating random numbers.

## Files

- `minesim.py` -- the simulator (Python); start here
- `minesim.go` -- the simulator (Go), about ten times faster
- `network` -- the default network configuration
- `gennetwork.py` -- generate a larger network configuration
- `blockint.py`, `blockint-count.py` -- block-interval sample generators

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

- Run minesim with the default configuration (`./minesim.py`)
- Do miners earn blocks in proportion to their hashrates?
  (`--chart` makes this easier to see)
- If not, are the differences due to chance?
  Are the results different if you run with:
  - Longer simulation (for example, `-h 10000000`)
  - Different seeds (for example, random seed `-s -1`)
  - Several seeds at once (for example, `-n 20`), so you can compare each
    difference against the spread across runs. Which differences survive?
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
    - The simulator warns you about this at startup; why can it tell in
      advance, just from the configuration file?
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
  - Draw a short run with `--dot` and compare the picture to the trace;
    find a seed that produces a fork (`-h 8 -s 21` is one)
- Auto-generate a network configuration file with hundreds or thousands of
  miners (`gennetwork.py` will do this), see what happens and whether the
  simulator scales reasonably.
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
