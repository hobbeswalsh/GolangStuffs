GolangStuffs
============

Golang work-in-progess stuffs

This repo is intended as a sandbox for my Go learning.

reflect.go
----------

A nameserver based on https://github.com/miekg/exdns/blob/master/reflect/reflect.go
This one also has support for looking up records in a SQLite database, as a proof-of-concept.


multifetch.go
-------------

A library to fetch URLs in parallel and report on the operation: status code, content, and time it took to fetch.
