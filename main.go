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
)

// myAddr is the public address of the server.
var myAddr string

// state is the current liveness status.
var state struct {
	mu sync.Mutex
	// livePeers indicates for each peer the time of last successful
	// heartbeat. The timestamp is zero (default time.Time) if the peer
	// is known but hasn't heartbeated yet.
	livePeers map[string]time.Time
}

func init() {
	state.livePeers = map[string]time.Time{}
}

func maybeAddPeer(peer string, isLive bool) {
	state.mu.Lock()
	defer state.mu.Unlock()

	ts := state.livePeers[peer]
	if isLive {
		log.Printf("peer is live: %s", peer)
		ts = time.Now()
	}
	state.livePeers[peer] = ts
}

// Handler for the /hello API.
func helloHandler(w http.ResponseWriter, req *http.Request) {
	from := req.URL.Query().Get("from")
	if from != "" {
		maybeAddPeer(from, true /*isLive*/)
	}

	w.Header().Add("Content-Type", "text/plain")
	io.WriteString(w, "hello!\n+ ")
	io.WriteString(w, myAddr)
	io.WriteString(w, "\n")
	state.mu.Lock()
	defer state.mu.Unlock()
	for peer, t := range state.livePeers {
		if peer == myAddr {
			continue
		}
		var zeroTime time.Time
		if t == zeroTime {
			// This peer has never heartbeaten yet. We don't know them.
			continue
		}
		if time.Now().Sub(t) < 5*time.Second {
			io.WriteString(w, "+ ")
		} else {
			io.WriteString(w, "- ")
		}
		io.WriteString(w, peer)
		io.WriteString(w, "\n")
	}
}

// Handler for the / endpoint. Useful to show in a browser.
func statusHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Add("Content-Type", "text/html")
	io.WriteString(w, "<html><body><p>This is node: ")
	io.WriteString(w, myAddr)
	io.WriteString(w, "</p><table border=1><thead><tr><th>peer</th><th>last seen</th><th>live?</th></tr></thead><tbody>")
	state.mu.Lock()
	defer state.mu.Unlock()
	for peer, t := range state.livePeers {
		isLive := time.Now().Sub(t) < 5*time.Second
		fmt.Fprintf(w, "<tr><td>%s</td><td>%v</td><td>%v</td></tr>\n",
			peer, t, isLive)
	}
	io.WriteString(w, "</tbody></table></body></html>\n")
}

// heartBeats runs in the background to update the liveness.
func heartBeats() {
	for {
		time.Sleep(2 * time.Second)
		checkAll()
	}
}

func checkAll() {
	state.mu.Lock()
	candidates := make([]string, 0, len(state.livePeers))
	for s := range state.livePeers {
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
}

func checkOne(srv string) {
	timeoutCtx, cleanup := context.WithTimeout(
		context.Background(), 1*time.Second)
	defer cleanup()

	url := "http://" + srv + "/hello?from=" + myAddr
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
	if len(parts) == 0 || parts[0] != "hello!" {
		log.Printf("invalid response: %s: %v", srv, parts)
		return
	}

	// Now also discover everyone else it knows about.
	for i, line := range parts[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "+ ") && !strings.HasPrefix(line, "- ") {
			log.Printf("invalid format: %s: %s", srv, line)
			continue
		}
		maybeAddr := line[2:]
		_, _, err := net.SplitHostPort(maybeAddr)
		if err != nil {
			log.Printf("invalid format: %s: %s: %v", srv, maybeAddr, err)
			continue
		}
		// The first line in the response has true liveness.
		// For every other line, the liveness bit may be
		// out of date now. Ignore it. Our own heartBeat will figure that
		// out later.
		maybeAddPeer(maybeAddr, i == 0 /*isLive*/)
	}
}

var myHostname = func() string {
	s, err := os.Hostname()
	if err != nil {
		log.Fatal(err)
	}
	return s
}()

var advAddr = flag.String("advertise", myHostname, "host to advertise")
var portNum = flag.Int("port", 8080, "port number to listen on")
var join = flag.String("join", "", "server to join")

func main() {
	flag.Parse()

	http.HandleFunc("/hello", helloHandler)
	http.HandleFunc("/", statusHandler)

	portPart := fmt.Sprintf(":%d", *portNum)
	myAddr = *advAddr + portPart

	for _, s := range strings.Split(*join, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		maybeAddPeer(s, false /*isLive*/)
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
