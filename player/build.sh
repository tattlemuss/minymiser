#!/usr/bin/env sh

rm -f *.prg
vasmm68k_mot example.s              -nosym -Ftos -o example.prg
vasmm68k_mot example.s -DDELTA_PACK -nosym -Ftos -o exampled.prg
