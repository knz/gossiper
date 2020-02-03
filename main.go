package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/cockroach/pkg/util/uuid"
)

// myID is the ID of this node.
var myID string

// myAddr is the public address of the server.
var myAddr string

// state is the current liveness status.
var state struct {
	mu sync.Mutex
	// livePeers indicates for each peer the time of last successful
	// heartbeat. The timestamp is zero (default time.Time) if the peer
	// is known but hasn't heartbeated yet.
	livePeers  map[string]time.Time
	decoPeers  map[string]struct{}
	knownAddrs map[string]struct{}
}

func init() {
	state.decoPeers = map[string]struct{}{}
	state.knownAddrs = map[string]struct{}{}
	state.livePeers = map[string]time.Time{}
}

func addPeerAddr(peerAddr string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.knownAddrs[peerAddr] = struct{}{}
}

func pingPeer(peerID string) {
	state.mu.Lock()
	defer state.mu.Unlock()

	ts := time.Now()
	if t := state.livePeers[peerID]; ts.Sub(t) > 1*time.Second {
		// We avoid refreshing a ping that's newer than 1 second
		// to avoid spamming the logging output when the same
		// node responds through multiple addresses.
		log.Printf("peer is live: %s", peerID)
	}
	state.livePeers[peerID] = ts
}

func decoPeer(peerID string) {
	if peerID == myID {
		log.Printf("received request to disconnect\n")
		os.Exit(0)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.decoPeers[peerID] = struct{}{}
}

func knownPeer(peerID string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if _, ok := state.decoPeers[peerID]; ok {
		return
	}
	t := state.livePeers[peerID]
	state.livePeers[peerID] = t
}

// Handler for the /bye API.
func byeHandler(w http.ResponseWriter, req *http.Request) {
	options := req.URL.Query()
	nodeID := options.Get("id")
	if nodeID != "" {
		decoPeer(nodeID)
	}
}

// Handler for the /hello API.
func helloHandler(w http.ResponseWriter, req *http.Request) {
	options := req.URL.Query()
	from := options.Get("from")
	if from != "" {
		pingPeer(from)
		peerAddr := options.Get("via")
		if peerAddr != "" {
			addPeerAddr(peerAddr)
		}
	}

	w.Header().Add("Content-Type", "text/plain")
	fmt.Fprintf(w, "hello %s\n", myID)
	state.mu.Lock()
	defer state.mu.Unlock()
	comma := ""
	for peerID := range state.livePeers {
		if peerID == myID {
			continue
		}
		io.WriteString(w, comma)
		io.WriteString(w, peerID)
		comma = " "
	}
	io.WriteString(w, "\n")
	comma = ""
	for peerID := range state.decoPeers {
		io.WriteString(w, comma)
		io.WriteString(w, peerID)
		comma = " "
	}
	io.WriteString(w, "\n")
	io.WriteString(w, myAddr)
	io.WriteString(w, "\n")
	for peer := range state.knownAddrs {
		if peer == myAddr {
			continue
		}
		io.WriteString(w, peer)
		io.WriteString(w, "\n")
	}
}

// Handler for the / endpoint. Useful to show in a browser.
func statusHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Add("Content-Type", "text/html")
	io.WriteString(w, "<html><body><p>This is node: ")
	fmt.Fprintf(w, "%s (%s)", myID, myAddr)
	io.WriteString(w, "</p><table border=1><thead><tr><th>peer</th><th>last seen</th><th>live?</th></tr></thead><tbody>\n")
	state.mu.Lock()
	defer state.mu.Unlock()
	for peer, t := range state.livePeers {
		if _, ok := state.decoPeers[peer]; ok {
			// Node is known-removed.
			continue
		}
		isLive := time.Now().Sub(t) < 5*time.Second
		fmt.Fprintf(w, "<tr><td>%s</td><td>%v</td><td>%v</td></tr>\n",
			peer, t, isLive)
	}
	io.WriteString(w, "</tbody></table><p>Peer addresses:</p><ul>\n")
	for p := range state.knownAddrs {
		fmt.Fprintf(w, "<li>%s</li>\n", p)
	}
	io.WriteString(w, "</ul></body></html>\n")
}

// heartBeats runs in the background to update the liveness.
func heartBeats() {
	for {
		time.Sleep(2 * time.Second)
		pingPeer(myID)
		checkAll()
	}
}

func checkAll() {
	state.mu.Lock()
	candidates := make([]string, 0, len(state.knownAddrs))
	for s := range state.knownAddrs {
		candidates = append(candidates, s)
	}
	state.mu.Unlock()

	var wg sync.WaitGroup
	for _, thisSrv := range candidates {
		wg.Add(1)
		srv := thisSrv
		go func() {
			defer wg.Done()
			checkOne(srv)
		}()
	}
	wg.Wait()

	state.mu.Lock()
	now := time.Now()
	for peerID, t := range state.livePeers {
		if _, ok := state.decoPeers[peerID]; ok {
			continue
		}
		if now.Sub(t) > 5*time.Second {
			log.Printf("peer is dead: %s", peerID)
		}
	}
	state.mu.Unlock()
}

func checkOne(srv string) {
	timeoutCtx, cleanup := context.WithTimeout(
		context.Background(), 1*time.Second)
	defer cleanup()

	url := "http://" + srv + "/hello?from=" + myID + "&via=" + myAddr
	req, err := http.NewRequestWithContext(timeoutCtx, "GET", url, nil)
	if err != nil {
		log.Printf("http: %s: %v", srv, err)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("http: %s: %v", srv, err)
		return
	}
	if resp.StatusCode != 200 {
		log.Printf("unexpected response: %s: %+v", srv, resp)
		return
	}
	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error reading response: %s: %v", srv, err)
		return
	}
	parts := strings.Split(string(bytes), "\n")
	if len(parts) < 3 || !strings.HasPrefix(parts[0], "hello ") {
		log.Printf("invalid response: %s: %v", srv, parts)
		return
	}
	peerID := parts[0][6:]
	pingPeer(peerID)
	for _, peerID := range strings.Split(parts[1], " ") {
		knownPeer(peerID)
	}
	for _, peerID := range strings.Split(parts[2], " ") {
		decoPeer(peerID)
	}

	// Now also discover everyone else it knows about.
	for _, line := range parts[3:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		_, _, err := net.SplitHostPort(line)
		if err != nil {
			log.Printf("invalid format: %s: %s: %v", srv, line, err)
			continue
		}
		addPeerAddr(line)
	}
}

var myHostname = func() string {
	s, err := os.Hostname()
	if err != nil {
		log.Fatal(err)
	}
	return s
}()

var nodeID = flag.String("id", uuid.MakeV4().Short(), "node ID")
var advAddr = flag.String("advertise", myHostname, "host to advertise")
var portNum = flag.Int("port", 8080, "port number to listen on")
var join = flag.String("join", "", "server to join")

func main() {
	flag.Parse()

	myID = *nodeID
	pingPeer(myID)

	http.HandleFunc("/hello", helloHandler)
	http.HandleFunc("/bye", byeHandler)
	http.HandleFunc("/", statusHandler)

	portPart := fmt.Sprintf(":%d", *portNum)
	myAddr = *advAddr + portPart

	for _, s := range strings.Split(*join, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		addPeerAddr(s)
	}

	ln, err := net.Listen("tcp", portPart)
	if err != nil {
		log.Fatal(err)
	}

	// Run the heartbeater in the background.
	go heartBeats()

	// Start this server.
	server := &http.Server{}
	log.Fatal(server.Serve(ln))
}
