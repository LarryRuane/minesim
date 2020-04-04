# minesim
## Cryptocurrency POW mining simulator

This program simulates a POW mining network. It _does_ simulate
- multiple mining nodes
- random poisson block intervals
- block forwarding (relaying)
- network peer topology
- various fixed delays from each peer to others
- varying hash power per miner

It does _not_ simulate
- transactions
- miners coming and going
- difficulty adjustment
- variable block rewards over time

The input file consists of one line per miner, specifying
- miner identifier (string)
- hashrate
- a list of peers, pairs of: miner id, relay delay

Empty lines and lines beginning with `#` are ignored.
