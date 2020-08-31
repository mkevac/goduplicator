goduplicator [![Build Status](https://travis-ci.org/mkevac/goduplicator.svg?branch=master)](https://travis-ci.org/mkevac/goduplicator)
============

Why you may need this?
----------------------
You may have production servers running, but you need to upgrade to a new system. You want to run A/B test on both old and new systems to confirm the new
system can handle the production load, and want to see whether the new system can run in shadow mode continuously without any issue.

It is similar to another project https://github.com/agnoster/duplicator, but faster on my tests and supports more than one mirror.

How it works?
-------------
goduplicator is a reverse proxy. It mirrors the data to all configured servers. The data from main server is sent back, but data from all other servers is ignored.

goduplicator is a TCP proxy, so it does not care which higher level protocol you are using.

Download binary
---------------
You can download binary files here: https://github.com/mkevac/goduplicator/releases

Install from source
-------------------
You would need Go language installed.
Just execute this command
```
go build
```
inside the source code directory

Usage
-----
```
./goduplicator -l ':8080' -f ':8081' -m ':8082' -m ':8083'
-l is a listening address
-f is an address of a main server
-m is an address of a mirror server (there could be more than one)
```

Comparison to agnoster/duplicator
---------------------------------
agnoster/duplicator on my tests gives about 46000 req/sec, while goduplicator gives approximately 76000 req/sec
Client and Server used are in cmd/ directory.

Server:
```
$ ./goduplicatortestserver
```

Another server:
```
$ ./goduplicatortestserver -l ':11001'
```

goduplicator:
```
$ ./goduplicator -l ':11002' -f ':11000' -m ':11001'
```

agnoster/duplicator:
```
$ duplicator -p 11002 -f 11000 -d 11001
```

Results for goduplicator:
```
$ ./goduplicatortestclient -a ':11002'
2015/09/04 20:07:49 71042 req/sec
2015/09/04 20:07:50 76552 req/sec
2015/09/04 20:07:51 76397 req/sec
2015/09/04 20:07:52 76691 req/sec
2015/09/04 20:07:53 76212 req/sec
2015/09/04 20:07:54 76555 req/sec
2015/09/04 20:07:55 76695 req/sec
2015/09/04 20:07:56 76797 req/sec
```

Results for agnoster/duplicator:
```
$ ./goduplicatortestclient -a ':11002'
2015/09/04 20:06:25 44803 req/sec
2015/09/04 20:06:26 46850 req/sec
2015/09/04 20:06:27 47002 req/sec
2015/09/04 20:06:28 46966 req/sec
2015/09/04 20:06:29 47248 req/sec
2015/09/04 20:06:30 46897 req/sec
2015/09/04 20:06:31 46590 req/sec
```
