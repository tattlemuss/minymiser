#!/usr/bin/env sh
SRC_PATH=.
CC=g++
LD=g++
CFLAGS=" -DDEBUG -I${SRC_PATH}/lib -std=c++11 -g"
LDFLAGS="-lc"

# Just build everything -- this project isn't big
${CC} ${CFLAGS} -c -o main.o main.cpp
mkdir -p bin
${LD} ${LDFLAGS} main.o -o bin/test


