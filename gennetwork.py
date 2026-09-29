#!/usr/bin/env python3

# Copyright (c) 2026-present Larry Ruane
# Distributed under the MIT software license, see
# https://www.opensource.org/licenses/mit-license.php.

"""Generate a minesim network topology file.

The `network` file in this repository was written by hand, which is fine for
six miners but not for the "auto-generate a network configuration file with
hundreds or thousands of miners" exercise in README.md. This writes one to
standard output, in exactly the format minesim reads.

Miners are divided into regions. Within a region, latency is low (--intra):
each miner is joined into a ring, so the region is certainly connected, and
then given extra random peers up to --peers. Between regions, latency is high
(--inter), and only the region's --gateways miners carry the traffic. That is
the same shape as the hand-written `network` file, just bigger.

Every connection is emitted in both directions, so every miner has inbound
peers and none of them ends up mining a private chain.

For example, two mining centers of 40 and 25 miners:

    ./gennetwork.py --region china:40 --region iceland:25 > big-network
    ./minesim.py -f big-network -h 20000
"""

import argparse
import random
import sys


def region_arg(text):
    """Parse a --region argument of the form NAME:COUNT."""
    name, _, count = text.partition(":")
    if not name or not count.isdigit() or int(count) < 1:
        raise argparse.ArgumentTypeError(
            f"expected NAME:COUNT with COUNT at least 1, got {text!r}")
    return name, int(count)


def hashrate_arg(text):
    """Parse a --hashrate argument of the form MIN:MAX (or a single number)."""
    low, _, high = text.partition(":")
    high = high or low
    if not low.isdigit() or not high.isdigit():
        raise argparse.ArgumentTypeError(
            f"expected MIN:MAX of whole numbers, got {text!r}")
    low, high = int(low), int(high)
    if low < 1 or high < low:
        raise argparse.ArgumentTypeError(
            f"expected 1 <= MIN <= MAX, got {text!r}")
    return low, high


def parse_args(argv=None):
    p = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--region", metavar="NAME:COUNT", type=region_arg,
                   action="append", required=True,
                   help="a mining region and how many miners are in it; "
                        "give this more than once for more regions")
    p.add_argument("--intra", metavar="SECONDS", type=float, default=0.5,
                   help="relay delay within a region (default: 0.5)")
    p.add_argument("--inter", metavar="SECONDS", type=float, default=15.0,
                   help="relay delay between regions (default: 15)")
    p.add_argument("--peers", metavar="N", type=int, default=4,
                   help="peers each miner has inside its own region "
                        "(default: 4)")
    p.add_argument("--gateways", metavar="N", type=int, default=2,
                   help="miners per region that connect to other regions "
                        "(default: 2)")
    p.add_argument("--hashrate", metavar="MIN:MAX", type=hashrate_arg,
                   default=(10, 1000),
                   help="range each miner's hashrate is drawn from "
                        "(default: 10:1000)")
    p.add_argument("--seed", metavar="N", type=int, default=0,
                   help="random number seed (default: 0)")
    args = p.parse_args(argv)

    if args.peers < 1:
        p.error("--peers must be at least 1")
    if args.gateways < 1:
        p.error("--gateways must be at least 1")
    if args.intra < 0 or args.inter < 0:
        p.error("delays must not be negative")
    return args


def build(args):
    """Return {miner name: {peer name: delay}}, both directions filled in."""
    regions = {}
    for name, count in args.region:
        if name in regions:
            sys.exit(f"gennetwork: duplicate region name: {name}")
        # Zero-pad so that the names sort the way a human expects.
        width = len(str(count - 1))
        regions[name] = [f"{name}{i:0{width}d}" for i in range(count)]

    links = {who: {} for names in regions.values() for who in names}

    def join(a, b, delay):
        """Connect two miners in both directions, keeping the lower delay."""
        if a == b:
            return
        for x, y in ((a, b), (b, a)):
            links[x][y] = min(delay, links[x].get(y, delay))

    for names in regions.values():
        # A ring first, so the region is certainly connected end to end.
        if len(names) > 1:
            for i, who in enumerate(names):
                join(who, names[(i + 1) % len(names)], args.intra)
        # Then extra random peers, up to --peers each.
        for who in names:
            others = [o for o in names if o != who and o not in links[who]]
            random.shuffle(others)
            for other in others[:max(0, args.peers - len(links[who]))]:
                join(who, other, args.intra)

    # Gateways carry all the traffic between regions.
    gateways = {name: names[:args.gateways] for name, names in regions.items()}
    for a in gateways:
        for b in gateways:
            if a < b:
                for x in gateways[a]:
                    for y in gateways[b]:
                        join(x, y, args.inter)

    return regions, gateways, links


def main():
    args = parse_args()
    random.seed(args.seed)
    regions, gateways, links = build(args)
    low, high = args.hashrate

    total = sum(len(names) for names in regions.values())
    print(f"# generated by gennetwork.py: {total} miners in "
          f"{len(regions)} region(s)")
    print(f"# {' '.join(f'{n}:{len(v)}' for n, v in regions.items())}"
          f"  intra {args.intra}  inter {args.inter}"
          f"  peers {args.peers}  gateways {args.gateways}"
          f"  hashrate {low}:{high}  seed {args.seed}")

    for name, names in regions.items():
        print()
        print(f"# {name} ({len(names)} miners, gateways: "
              f"{' '.join(gateways[name])})")
        for who in names:
            peers = " ".join(f"{p} {d:g}" for p, d in sorted(links[who].items()))
            print(f"{who} {random.randint(low, high)} {peers}")


if __name__ == "__main__":
    main()
