goduplicator
============

Why you may need this?
----------------------
You may have production servers running, but you need to upgrade to a new system. You want to run A/B test on both old and new systems to confirm the new
system can handle the production load, and want to see whether the new system can run in shadow mode continuously without any issue.

How it works?
-------------
goduplicator is a reverse proxy. It mirrors the data to all configured servers. The data from main server is sent back, but data from all other servers is ignored.

goduplicator is a TCP proxy, so it does not care which higher level protocol you are using.

Download binary
---------------

Install from source
-------------------
You would need Go language installed. Just execute this command:
```
go get github.com/mkevac/goduplicator
```

Usage
-----
```
./goduplicator -l ':8080' -f ':8081' -m ':8082' -m ':8083'
-l is a listening address
-f is am address of a main server
-m is an address of a mirror server (there could be more than one)
```
