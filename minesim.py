#!/usr/bin/env python3

# Copyright (c) 2020-present Larry Ruane
# Distributed under the MIT software license, see
# https://www.opensource.org/licenses/mit-license.php.

# This work has been supported by a Brink grant (https://brink.dev/)

"""Simulate a network of block miners in a proof of work system.

You specify a network topology, and a hash rate for each miner.
The time units are arbitrary, but seconds works well.

This is a Python version of minesim.go. The two programs take the same
arguments, read the same network configuration files, and print the same
statistics; this one additionally offers --format, --chart, --dot and -n.
Their numbers differ for a given seed because Go and Python have different
random number generators.
"""

import sys

if sys.version_info < (3, 10):
    sys.exit("minesim needs Python 3.10 or later")

import argparse
import copy
import csv
import heapq
import json
import math
import random
import signal
import statistics
import time
from dataclasses import dataclass

# Die quietly when whatever is reading our output goes away, the way an
# ordinary Unix program does. The trace output is long enough that piping it
# to head or less is the normal thing to do, and quitting less early
# shouldn't produce a Python traceback. (Windows has no SIGPIPE.)
if hasattr(signal, "SIGPIPE"):
    signal.signal(signal.SIGPIPE, signal.SIG_DFL)

# Block ids and heights are both plain integers, but they mean different
# things: heights can repeat (that is what a fork is), while a block id is
# unique. Block ids start well above any height we're likely to reach so
# that the two can't be confused when reading the trace output.
BlockId = int
Height = int

FIRSTBLOCKID = 1000

# How much the tallest chain must grow before we finalize old blocks again.
CLEAN_INTERVAL = 10000

# --dot draws every block ever mined, so it's only useful for tiny runs.
DOT_MAX_HEIGHT = 2000


@dataclass(slots=True)
class Block:
    parent: BlockId  # the genesis block is the only one with no parent
    height: Height   # more than one block can have this height
    miner: int       # which miner found this block (-1 for genesis)
    time: float      # time this block was mined


@dataclass(slots=True)
class Peer:
    """An outbound connection: we forward blocks to this miner."""

    miner: int   # index into Simulator.miners
    delay: float # time for a block to get there, including verification


@dataclass(slots=True)
class Miner:
    name: str
    index: int                    # in Simulator.miners
    hashrate: int                 # how much hashing power this miner has
    peers: list[Peer]             # outbound peers
    tip: BlockId = FIRSTBLOCKID   # the block we're trying to mine onto
    mined: Height = 0             # blocks mined so far, for the trace output
    minedfinal: Height = 0        # of those, how many are no longer reorgable
    credit: Height = 0            # best-chain blocks we've mined


@dataclass(order=True, slots=True)
class Event:
    """The arrival of a block, either mined by us or relayed from a peer.

    "when" comes first so that the event list, a heap, orders by arrival
    time; the remaining fields only break ties, and only to keep the
    ordering deterministic.
    """

    when: float
    to: int      # which miner (index) gets the block
    mining: bool # arrival from our own mining (True) or from a peer (False)
    bid: BlockId # block being mined on (its parent), or block from a peer


def warn(message):
    """Report a problem that doesn't stop the simulation.

    This goes to stderr so that --format json and --format csv output stays
    clean enough to pipe into another program.
    """
    print(f"minesim: warning: {message}", file=sys.stderr)


def div(num, den):
    """Divide, or return NaN if the denominator is zero.

    A zero denominator means a miner never got a block onto the best chain,
    which happens when it's isolated from the rest of the network. The Go
    version produces NaN here rather than failing, so we do the same.
    """
    return math.nan if den == 0 else num / den


def pct(num, den):
    """num as a percentage of den, or NaN if den is zero."""
    return div(num * 100.0, den)


def json_safe(value):
    """Replace NaN with None (null), since JSON has no way to write NaN."""
    if isinstance(value, float) and not math.isfinite(value):
        return None
    if isinstance(value, dict):
        return {k: json_safe(v) for k, v in value.items()}
    if isinstance(value, list):
        return [json_safe(v) for v in value]
    return value


def read_network(path):
    """Read the network topology file; return a list of Miner in file order.

    Each line is a miner name, its hashrate, and then a list of pairs of
    peer name and the delay to send a block to that peer. Empty lines and
    lines whose first token starts with "#" are ignored.
    """
    try:
        with open(path) as f:
            lines = [line.split() for line in f]
    except OSError as e:
        sys.exit(f"minesim: open failed: {e}")
    lines = [words for words in lines if words and not words[0].startswith("#")]

    # Number the miners first, because a line can name a peer that isn't
    # defined until later in the file.
    index = {}
    for words in lines:
        if words[0] in index:
            sys.exit(f"minesim: duplicate miner name: {words[0]}")
        index[words[0]] = len(index)
    if not index:
        sys.exit("minesim: no miners")

    miners = []
    for words in lines:
        name, rest = words[0], words[1:]
        if not rest:
            sys.exit(f"minesim: missing hashrate: {name}")
        try:
            hashrate = int(rest[0])
        except ValueError:
            sys.exit(f"minesim: bad hashrate: {rest[0]}")
        if hashrate <= 0:
            sys.exit(f"minesim: hashrate must be greater than zero: {rest[0]}")

        rest = rest[1:]
        if len(rest) % 2:
            sys.exit(f"minesim: bad peer delay pairs: {name} {' '.join(rest)}")
        peers = []
        for peername, delay in zip(rest[::2], rest[1::2]):
            if peername not in index:
                sys.exit(f"minesim: no such miner: {peername}")
            try:
                peers.append(Peer(index[peername], float(delay)))
            except ValueError:
                sys.exit(f"minesim: bad delay: {delay}")
        miners.append(Miner(name=name, index=index[name],
                            hashrate=hashrate, peers=peers))
    return miners


def lint_network(miners):
    """Warn about configuration mistakes that give confusing results."""
    inbound = [0] * len(miners)
    for m in miners:
        seen = set()
        for p in m.peers:
            inbound[p.miner] += 1
            if p.miner == m.index:
                warn(f"miner {m.name} lists itself as a peer")
            elif p.miner in seen:
                warn(f"miner {m.name} lists peer {miners[p.miner].name} "
                     "more than once")
            seen.add(p.miner)
    for m in miners:
        if inbound[m.index] == 0:
            warn(f"miner {m.name} has no inbound peers, so it will never hear "
                 "about anyone else's blocks; it will mine its own private "
                 "chain and the statistics will come out as NaN")


class Simulator:
    """One run of the simulation, from the genesis block to the stop height."""

    def __init__(self, miners, blockinterval, stopheight, seed=0,
                 traceenable=False, prunedepth=0,
                 cleaninterval=CLEAN_INTERVAL):
        # Arguments:
        self.miners = miners
        self.seed = seed
        self.blockinterval = blockinterval
        self.stopheight = stopheight
        self.traceenable = traceenable
        self.prunedepth = prunedepth
        self.cleaninterval = cleaninterval

        # Main simulator state:
        self.currenttime = 0.0  # simulated time since the start
        self.blocks = [Block(parent=0, height=0, miner=-1, time=0.0)]
        self.eventlist = []     # priority queue, lowest timestamp first

        # Implementation detail simulator state:
        self.rng = random.Random(seed)  # for block interval calculation
        self.totalhash = sum(m.hashrate for m in miners)
        self.baseblockid = FIRSTBLOCKID  # blocks[0] has this block id
        self.maxheight = 0      # greatest height any miner has reached
        self.maxreorg = 0       # greatest depth reorg
        self.mined = 0          # blocks mined below baseblockid
        self.pruned = False     # has prunedepth ever had to step in?

    def trace(self, fmt, *args):
        """Show one step of the simulation, if tracing is enabled.

        The arguments are formatted with "%" rather than an f-string on
        purpose: this is called for every event, and this way the work of
        formatting only happens when the output is actually wanted.
        """
        if self.traceenable:
            print(fmt % args)

    def valid_block(self, bid):
        return self.baseblockid <= bid < self.baseblockid + len(self.blocks)

    def get_block(self, bid):
        return self.blocks[bid - self.baseblockid]

    def get_height(self, bid):
        return self.blocks[bid - self.baseblockid].height

    def relay(self, mi, newblockid):
        """Relay a block we just mined or just heard about to our peers.

        This also sends to the peer we received the block from, if it's one
        of our peers, but that's okay: it will be ignored.
        """
        newheight = self.get_height(newblockid)
        for p in self.miners[mi].peers:
            # Improve simulator efficiency by not relaying blocks that are
            # certain to be ignored. A miner's tip height never decreases,
            # so a peer already this high will never want this block.
            if self.get_height(self.miners[p.miner].tip) < newheight:
                heapq.heappush(self.eventlist, Event(
                    when=self.currenttime + p.delay,
                    to=p.miner, mining=False, bid=newblockid))

    def start_mining(self, mi, bid):
        """Start mining on top of the given existing block."""
        m = self.miners[mi]
        m.tip = bid

        # Schedule an event for when our "mining" will be done. Solve times
        # follow the Poisson distribution; blockint.py shows where this
        # expression comes from. A miner with a tenth of the network's hash
        # power takes, on average, ten block intervals to find a block.
        solvetime = (-math.log(1.0 - self.rng.random()) *
                     self.blockinterval * self.totalhash / m.hashrate)
        heapq.heappush(self.eventlist, Event(
            when=self.currenttime + solvetime,
            to=mi, mining=True, bid=bid))
        self.trace(
            "%.3f %s start-on %d height %d mined %d credit %d solve %.2f",
            self.currenttime, m.name, bid, self.get_height(bid),
            m.mined, m.credit, solvetime)

    def reorg_depth(self, fromtip, totip):
        """How many blocks we back up to switch from one chain to another."""
        # Move back on the better chain until it's level with ours, then
        # step both back together until the two branches meet.
        while self.get_height(totip) > self.get_height(fromtip):
            totip = self.get_block(totip).parent
        depth = 0
        while totip != fromtip:
            depth += 1
            totip = self.get_block(totip).parent
            fromtip = self.get_block(fromtip).parent
        return depth

    def set_base(self, newbase):
        """Count and discard every block below newbase, crediting the miners.

        Note the asymmetry: newbase itself is credited here (it's certainly
        on the best chain) but is not counted as mined, because it isn't
        being dropped yet. It gets counted by the next call, when it does
        drop. The very last base block is counted once by results().
        """
        bid = newbase
        while bid != self.baseblockid:
            b = self.get_block(bid)
            self.miners[b.miner].credit += 1
            bid = b.parent

        # Count the blocks about to be dropped, best-chain and stale alike,
        # but not the genesis block, which nobody mined.
        for b in self.blocks[:newbase - self.baseblockid]:
            if b.height > 0:
                self.mined += 1
                self.miners[b.miner].minedfinal += 1

        self.blocks = self.blocks[newbase - self.baseblockid:]
        self.baseblockid = newbase

    def clean_blocks(self):
        """Finalize and discard the blocks that can no longer be reorged away.

        Everything below the point where all the miners' chains agree is
        settled, so it can be counted, credited, and forgotten.
        """
        # Move down from every tip until they're all at the same height.
        minheight = min(self.get_height(m.tip) for m in self.miners)
        tips = [m.tip for m in self.miners]
        for i, bid in enumerate(tips):
            while self.get_height(bid) > minheight:
                bid = self.get_block(bid).parent
            tips[i] = bid

        # Then walk them down together until they meet: the oldest branch
        # point, which is the newest block that is certainly permanent.
        while any(bid != tips[0] for bid in tips):
            tips = [self.get_block(bid).parent for bid in tips]
        self.set_base(tips[0])

    def force_prune(self):
        """Bound memory when the miners' chains never converge.

        clean_blocks() can only discard blocks that every miner agrees on,
        so a single miner that is permanently far behind -- almost always
        one with no inbound peers, which lint_network() warns about --
        stops anything from ever being freed, and memory grows without
        limit. When that happens we drop the oldest blocks anyway and move
        the stragglers onto the new base, as though they had finally heard
        about the better chain.
        """
        # Back off half the limit from the tallest tip, so that this doesn't
        # have to run again on the very next block.
        newbase = max((m.tip for m in self.miners), key=self.get_height)
        target = self.get_height(newbase) - self.prunedepth // 2
        while self.get_height(newbase) > target and newbase != self.baseblockid:
            newbase = self.get_block(newbase).parent
        if newbase == self.baseblockid:
            return  # nothing to drop; every branch is short but there are many

        newheight = self.get_height(newbase)
        for m in self.miners:
            bid = m.tip
            while self.get_height(bid) > newheight:
                bid = self.get_block(bid).parent
            if bid != newbase:
                if not self.pruned:
                    self.pruned = True
                    warn(f"miner {m.name} fell more than {self.prunedepth} "
                         "blocks behind, so --prune-depth moved it onto the "
                         "best chain to keep memory bounded; its statistics "
                         "will not be meaningful")
                self.start_mining(m.index, newbase)
        self.set_base(newbase)

    def run(self):
        """Run until some miner reaches the stopping height."""
        # Start every miner off mining on the genesis block.
        for mi in range(len(self.miners)):
            self.start_mining(mi, self.baseblockid)

        cleanedat = 0  # the height when we last called clean_blocks()
        while self.maxheight < self.stopheight:
            if (self.cleaninterval and
                    self.maxheight - cleanedat >= self.cleaninterval):
                cleanedat = self.maxheight
                self.clean_blocks()
            if self.prunedepth and len(self.blocks) > self.prunedepth:
                self.force_prune()

            ev = heapq.heappop(self.eventlist)
            self.currenttime = ev.when
            mi = ev.to
            m = self.miners[mi]
            bid = ev.bid

            if ev.mining:
                # We mined a block, unless this is a stale mining event,
                # meaning we've since switched to a better block. In that
                # case we already have another mining event outstanding.
                if bid != m.tip:
                    continue
                m.mined += 1
                newheight = self.get_height(m.tip) + 1
                if self.maxheight < newheight:
                    self.maxheight = newheight
                bid = self.baseblockid + len(self.blocks)  # id of the new block
                self.blocks.append(Block(parent=m.tip, height=newheight,
                                         miner=mi, time=self.currenttime))
                self.trace("%.3f %s mined-newid %d on %d height %d",
                           self.currenttime, m.name, bid, m.tip, newheight)
            else:
                # A block from a peer, which may be stale: either it's
                # already been discarded, or we're mining on one at least
                # as good.
                if (not self.valid_block(bid) or
                        self.get_height(bid) <= self.get_height(m.tip)):
                    continue
                # This block is better, so switch to it; first work out how
                # far back we have to go to get onto its chain.
                self.trace("%.3f %s received-switch-to %d",
                           self.currenttime, m.name, bid)
                reorg = self.reorg_depth(m.tip, bid)
                if reorg > 0:
                    self.trace("%.3f %s reorg %d maxreorg %d",
                               self.currenttime, m.name, reorg, self.maxreorg)
                if self.maxreorg < reorg:
                    self.maxreorg = reorg

            self.relay(mi, bid)
            self.start_mining(mi, bid)

    def results(self):
        """Settle the accounting and return the statistics as a dict."""
        self.clean_blocks()
        base = self.blocks[0]
        bestchain = base.height

        # The base block is mined and on the best chain, but it's the one
        # block clean_blocks() never drops, so count it here. (Getting this
        # wrong is what made the Go version report a negative stale count
        # for short runs.)
        mined = self.mined
        minedfinal = [m.minedfinal for m in self.miners]
        if bestchain > 0:
            mined += 1
            minedfinal[base.miner] += 1
        stale = mined - bestchain

        return {
            "seed_arg": self.seed,
            "block_interval_arg": self.blockinterval,
            "stopheight_arg": self.stopheight,
            "total_hashrate_arg": self.totalhash,
            "mined_blocks": mined,
            "best_chain_blocks": bestchain,
            "total_simtime": base.time,
            "ave_block_time": div(base.time, bestchain),
            "stale_blocks": stale,
            "stale_rate": pct(stale, mined),
            "max_reorg_depth": self.maxreorg,
            "miners": [{
                "name": m.name,
                "hashrate": m.hashrate,
                "hashrate_pct": pct(m.hashrate, self.totalhash),
                "mined_blocks": minedfinal[m.index],
                "credit_blocks": m.credit,
                "blocks_pct": pct(m.credit, bestchain),
                "stale_rate": pct(minedfinal[m.index] - m.credit,
                                  minedfinal[m.index]),
            } for m in self.miners],
        }


def print_text(r):
    """The results table, in the same layout as the Go version's."""
    print(f"{'seed-arg':<20} {r['seed_arg']:14d}")
    print(f"{'block-interval-arg':<20} {r['block_interval_arg']:14d}")
    print(f"{'stopheight-arg':<20} {r['stopheight_arg']:14d}")
    print(f"{'total-hashrate-arg':<20} {r['total_hashrate_arg']:14d}")
    print(f"{'mined-blocks':<20} {r['mined_blocks']:14d}")
    print(f"{'total-simtime':<20} {r['total_simtime']:14.3f}")
    print(f"{'ave-block-time':<20} {r['ave_block_time']:14.3f}")
    print(f"{'stale-blocks':<20} {r['stale_blocks']:14d}")
    print(f"{'stale-rate':<20} {r['stale_rate']:14.2f}%")
    print(f"{'max-reorg-depth':<20} {r['max_reorg_depth']:14d}")
    for m in r["miners"]:
        print(f"miner {m['name']:<13}  hashrate-arg {m['hashrate']:6d} "
              f"{m['hashrate_pct']:6.2f}% "
              f"blocks {m['blocks_pct']:6.2f}% "
              f"stale-rate {m['stale_rate']:6.2f}%")


def print_chart(runs, width=34):
    """Bar chart of each miner's hash power against the blocks it actually won.

    This is the point of the whole simulator: a well-connected miner wins
    more than its share of the blocks, and a poorly-connected one wins less.
    With several runs, "won" is the mean over all of them.
    """
    print()
    print("hashrate share (hash) vs best-chain blocks won (won)" +
          (f", mean of {len(runs)} runs" if len(runs) > 1 else ""))
    miners = runs[0]["miners"]
    won = [statistics.fmean(r["miners"][i]["blocks_pct"] for r in runs)
           for i in range(len(miners))]
    scale = max(v for m, w in zip(miners, won) for v in (m["hashrate_pct"], w)
                if math.isfinite(v))
    for m, w in zip(miners, won):
        for label, value in (("hash", m["hashrate_pct"]), ("won", w)):
            if math.isfinite(value):
                bar = "#" * round(value / scale * width)
                shown = f"{value:6.2f}%"
            else:
                bar, shown = "", "   n/a"
            delta = ""
            if label == "won" and math.isfinite(value):
                delta = f"  {value - m['hashrate_pct']:+.2f}"
            print(f"{m['name'] if label == 'hash' else '':<14}"
                  f"{label:<5}|{bar:<{width}}| {shown}{delta}")


def print_aggregate(runs):
    """Mean and standard deviation over several runs with different seeds.

    This is how to tell whether a miner's shortfall is a real effect of the
    network topology or just luck: if the gap between hashrate share and
    blocks won is smaller than the spread, it's luck.
    """
    def meansd(values):
        mean = statistics.fmean(values)
        sd = statistics.stdev(values) if len(values) > 1 else 0.0
        return mean, sd

    seeds = f"{runs[0]['seed_arg']}..{runs[-1]['seed_arg']}"
    first = runs[0]
    print(f"{'runs':<20} {len(runs):14d}")
    print(f"{'seed-args':<20} {seeds:>14}")
    print(f"{'block-interval-arg':<20} {first['block_interval_arg']:14d}")
    print(f"{'stopheight-arg':<20} {first['stopheight_arg']:14d}")
    print(f"{'total-hashrate-arg':<20} {first['total_hashrate_arg']:14d}")
    for label, key in (("ave-block-time", "ave_block_time"),
                       ("stale-rate", "stale_rate")):
        mean, sd = meansd([r[key] for r in runs])
        suffix = "%" if key == "stale_rate" else " "
        print(f"{label:<20} {mean:14.3f}{suffix} +/- {sd:.3f}")
    print(f"{'max-reorg-depth':<20} "
          f"{max(r['max_reorg_depth'] for r in runs):14d}  (over all runs)")
    for i, m in enumerate(first["miners"]):
        won, wonsd = meansd([r["miners"][i]["blocks_pct"] for r in runs])
        stale, stalesd = meansd([r["miners"][i]["stale_rate"] for r in runs])
        print(f"miner {m['name']:<13}  hashrate-arg {m['hashrate']:6d} "
              f"{m['hashrate_pct']:6.2f}% "
              f"blocks {won:6.2f}% +/- {wonsd:4.2f} "
              f"stale-rate {stale:6.2f}% +/- {stalesd:4.2f}")


def print_csv(runs, out=sys.stdout):
    """One row per miner per run, for loading into a spreadsheet or script."""
    fields = ["seed", "block_interval", "stopheight", "mined_blocks",
              "ave_block_time", "stale_blocks", "stale_rate",
              "max_reorg_depth", "miner", "hashrate", "hashrate_pct",
              "blocks_pct", "miner_stale_rate"]
    w = csv.DictWriter(out, fieldnames=fields)
    w.writeheader()
    for r in runs:
        for m in r["miners"]:
            w.writerow({
                "seed": r["seed_arg"],
                "block_interval": r["block_interval_arg"],
                "stopheight": r["stopheight_arg"],
                "mined_blocks": r["mined_blocks"],
                "ave_block_time": round(r["ave_block_time"], 3),
                "stale_blocks": r["stale_blocks"],
                "stale_rate": round(r["stale_rate"], 4),
                "max_reorg_depth": r["max_reorg_depth"],
                "miner": m["name"],
                "hashrate": m["hashrate"],
                "hashrate_pct": round(m["hashrate_pct"], 4),
                "blocks_pct": round(m["blocks_pct"], 4),
                "miner_stale_rate": round(m["stale_rate"], 4),
            })


def print_dot(sim):
    """Draw the block tree with Graphviz, so forks can actually be seen.

    Best-chain blocks are one color and stale blocks another; the arrows
    point from each block to its child, so time runs left to right. Only useful for a short run -- see DOT_MAX_HEIGHT.
    """
    # The best chain is the one ending at the tallest block; where two
    # blocks tie, the one mined first wins, which is what max() gives us.
    best = max(range(len(sim.blocks)), key=lambda i: sim.blocks[i].height)
    onchain = set()
    bid = sim.baseblockid + best
    while True:
        onchain.add(bid)
        b = sim.get_block(bid)
        if b.height == 0:
            break
        bid = b.parent

    print("digraph blocks {")
    print("  rankdir=LR;")
    print('  node [shape=box, style=filled, fontname="monospace"];')
    for i, b in enumerate(sim.blocks):
        bid = sim.baseblockid + i
        who = "genesis" if b.miner < 0 else sim.miners[b.miner].name
        color = "lightblue" if bid in onchain else "mistyrose"
        print(f'  b{bid} [label="{bid}\\nheight {b.height}\\n{who}", '
              f"fillcolor={color}];")
        if b.height > 0:
            print(f"  b{b.parent} -> b{bid};")
    print("}")


def parse_args(argv=None):
    p = argparse.ArgumentParser(
        add_help=False,  # so that -h can keep its meaning from minesim.go
        description="Simulate a proof-of-work mining network.",
        epilog="See README.md for the network configuration file format.")
    p.add_argument("--help", action="help",
                   help="show this help message and exit")
    p.add_argument("-f", dest="network", metavar="FILE", default="./network",
                   help="network topology file (default: ./network)")
    p.add_argument("-i", dest="blockinterval", metavar="N", type=int,
                   default=600, help="average block interval (default: 600)")
    p.add_argument("-h", dest="stopheight", metavar="N", type=int,
                   default=1_000_000,
                   help="stopping height (default: 1000000)")
    p.add_argument("-t", dest="traceenable", action="store_true",
                   help="print execution trace to stdout")
    p.add_argument("-s", dest="seed", metavar="N", type=int, default=0,
                   help="random number seed, -1 to use wall-clock")
    p.add_argument("-n", dest="runs", metavar="N", type=int, default=1,
                   help="repeat with N successive seeds and report the mean "
                        "and standard deviation")
    p.add_argument("--format", choices=("text", "json", "csv"), default="text",
                   help="how to print the results (default: text)")
    p.add_argument("--chart", action="store_true",
                   help="add a bar chart of hashrate share vs blocks won")
    p.add_argument("--dot", action="store_true",
                   help="draw the block tree as Graphviz instead of "
                        "reporting statistics")
    p.add_argument("-p", "--prune-depth", dest="prunedepth", metavar="N",
                   type=int, default=100_000,
                   help="bound memory if the miners' chains never converge, "
                        "0 for no limit (default: 100000)")
    args = p.parse_args(argv)

    if args.blockinterval <= 0:
        p.error("-i must be greater than zero")
    if args.stopheight <= 0:
        p.error("-h must be greater than zero")
    if args.runs < 1:
        p.error("-n must be at least 1")
    if args.prunedepth < 0:
        p.error("--prune-depth must not be negative")
    if args.chart and args.format != "text":
        p.error("--chart only works with --format text")
    if args.dot and args.stopheight > DOT_MAX_HEIGHT:
        p.error(f"--dot keeps and draws every block, so it needs "
                f"-h {DOT_MAX_HEIGHT} or less")
    return args


def main():
    args = parse_args()
    seed = time.time_ns() if args.seed == -1 else args.seed

    miners = read_network(args.network)
    lint_network(miners)

    runs = []
    for i in range(args.runs):
        sim = Simulator(
            copy.deepcopy(miners), args.blockinterval, args.stopheight,
            seed=seed + i,
            traceenable=args.traceenable,
            # --dot needs the whole tree, so nothing may be discarded.
            prunedepth=0 if args.dot else args.prunedepth,
            cleaninterval=0 if args.dot else CLEAN_INTERVAL)
        sim.run()
        if args.dot:
            print_dot(sim)
            return
        runs.append(sim.results())

    if args.format == "json":
        json.dump(json_safe(runs if len(runs) > 1 else runs[0]), sys.stdout,
                  indent=2, allow_nan=False)
        print()
    elif args.format == "csv":
        print_csv(runs)
    elif len(runs) > 1:
        print_aggregate(runs)
    else:
        print_text(runs[0])

    if args.chart:
        print_chart(runs)


if __name__ == "__main__":
    main()
