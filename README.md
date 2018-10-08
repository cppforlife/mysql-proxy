## mysql-proxy

MySQL proxy that terminates TLS before proxying connection to MySQL server (without TLS). It does not require any changes to server or clients.

```bash
$ go build proxy.go
$ ./proxy 3306 3307 ./server.crt ./server.key
```

## Protocol

### Without TLS packets

Proxy forwards initial handshakes and auth results without any modifications.

```
Server     Proxy      Client
  <-----------------------        tcp connection initiated
  ----------------------->        server sends handshake packet
  <-----------------------        client responds with full handshake (with hashed password)
  ----------------------->        if password is correct, reply with auth ok
  (packet seq reset to 0)
  (copying both ways)
```

### With TLS packets

Without proxy:

```
Server                Client
  <-----------------------        tcp connection initiated
  ----------------------->        server sends handshake packet
  <-----------------------        client responds with short handshake
  <-----------------------        tls connection initialized
  <-----------------------        client responds with full handshake
  ----------------------->        if password is correct, reply with auth ok
  (packet seq reset to 0)
  (copying both ways)
```

Proxy drops client's short handshake and rewrites full handshake packet to disable SSL.

```
Server     Proxy      Client
  <-----------------------        tcp connection initiated
  ----------------------->        server sends handshake packet
             <------------        client responds with short handshake
                                  proxy will drop short handshake
                                  proxy prepares to receive tls handshake from client
             <------------        tls connection initialized
  <-------- ~~~ <---------        client responds with full handshake
                                  proxy rewrites packet seq number
                                  proxy removes "client supports ssl" flag
  --------> ~~~ --------->        if password is correct, server replies with auth ok
                                  proxy rewrites packet seq number
  (packet seq reset to 0)
  (copying both ways)
```

Reference:

- [go-sql-driver/mysql's writeHandshakeResponsePacket](https://github.com/go-sql-driver/mysql/blob/7ac0064e822156a17a6b598957ddf5e0287f8288/packets.go#L246)
- [mysql's packet-Protocol::SSLRequest](https://dev.mysql.com/doc/internals/en/connection-phase-packets.html#packet-Protocol::SSLRequest)

## TODO

- add connection deadlines
- set connection tcp keepalive
