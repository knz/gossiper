# Gossiper - a simple distributed server with gossip liveness

`gossiper` runs one node for a distributed liveness store.

The liveness store maintains (using a gossip protocol) its list of
participants and for each participant a last heartbeat timestamp.

## Starting a node

The following command-line parameters are recognized:

- `-port`: port number to listen on for HTTP. Default 8080.
- `-advertise`: public hostname to advertise to peers. Defaults to
  `os.Hostname()`. Should be customized when running on multiple IP
  nodes.
- `-join`: one or more comma-separate addr:port pairs of other nodes
  to join. May be empty to indicate to simply wait for other peers to
  join this node.

## API Server

Each node offers the following API over HTTP:

- `/` (browser index page): a HTML page reporting the current
  database.

- `/hello`: the liveness map in a precise textual format — this is
  used by peers to run the gossip protocol. Example output:

  ```
  hello!
  + host1:8080
  - host2:8081
  ```

  The first line in the response is always `hello!`.

  The second line in the response is always "`+ `" followed by the public address of the peer.
  All the lines after that are the other peers known to this peer, with their current liveness
  status (`+` to indicate live, `-` to indicate dead).
