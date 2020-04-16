#!/usr/bin/python3
# Block interval simulator
# https://en.wikipedia.org/wiki/Poisson_distribution

import sys, random, math

if len(sys.argv) != 3:
    print('usage: ', sys.argv[0], 'n average')
    exit(1)

n = int(sys.argv[1])
ave = int(sys.argv[2])

print('[', end='')
for i in range(n):
    print(round(-math.log(1.0-random.random())*ave), end=', ')
print(']')
