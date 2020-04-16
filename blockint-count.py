#!/usr/bin/env python3
# Block interval simulator using counting (slow)
# https://en.wikipedia.org/wiki/Poisson_distribution
#
# This is an inefficient version that closely resembles
# actual mining. See blockint.py for a better version.

import sys, random, math

if len(sys.argv) != 3:
    print('usage: ', sys.argv[0], 'n average')
    exit(1)

n = int(sys.argv[1])
ave = int(sys.argv[2])

print('[', end='')
for i in range(n):
    counter = 1
    while random.randrange(ave) > 0:
        counter += 1
    print(counter, end=', ')
print(']')
