# Gossiper - a simple distributed server with gossip liveness

`gossiper` runs one node for a distributed liveness store.

The liveness store maintains (using a gossip protocol) its list of
participants and for each participant a last heartbeat timestamp.

## Starting a node

The following command-line parameters are recognized:

- `-id`: node ID. If not specified, a random ID is used. This is useful
  to simulate the same node going off and on again.
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

- `/bye?id=NNN` mark node NNN as decommissioned. If the node
  is still running, it will terminate when it learns of its status.

- `/hello`: The liveness endpoint.  This is
  used in the peer-to-peer gossip protocol but can also serve
  interactive demos using `curl` in the terminal.

  Example output:

  ```
  hello 4399d1f6
  abc 4399d1f6
  cde xxqqqw
  host1:8080
  host2:8081
  ```

  The first line in the response is always `hello ` followed by the
  peer's node ID.

  The second line is the list of known non-decommissioned peer IDs
  throughout the cluster.

  The third line is the list of decommissioned peer IDs throughout the
  cluster.

  All the lines after that are the other peer addresses known to this
  peer.
