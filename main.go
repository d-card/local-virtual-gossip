package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	mathrand "math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const topicName = "gossipsub-test"

// Global log buffer and coordination
var (
	logBuffer            []string
	receivedFirstMessage bool
	flushOnce            sync.Once
	logLock              sync.Mutex
)

func logWithTime(format string, a ...interface{}) {
	timestamp := time.Now().Format(time.RFC3339Nano)
	line := fmt.Sprintf("[%s] %s", timestamp, fmt.Sprintf(format, a...))

	logLock.Lock()
	defer logLock.Unlock()
	logBuffer = append(logBuffer, line)
	fmt.Print(line) // Optional: print to stdout for real-time debug
}

func flushLogToDisk(nodeNum int) {
	logLock.Lock()
	defer logLock.Unlock()

	os.MkdirAll("logs", 0755)
	logPath := fmt.Sprintf("logs/node%d.log", nodeNum)
	f, err := os.Create(logPath)
	if err != nil {
		fmt.Printf("Error creating log file: %v\n", err)
		return
	}
	defer f.Close()

	for _, line := range logBuffer {
		f.WriteString(line)
	}
	logWithTime("Flushed %d log lines to %s\n", len(logBuffer), logPath)
}

func handleMessages(sub *pubsub.Subscription, nodeNum int) {
	for {
		msg, err := sub.Next(context.Background())
		if err != nil {
			log.Fatal(err)
		}
		logWithTime("Received message from %s: %s\n", msg.ReceivedFrom, string(msg.Data))

		if !receivedFirstMessage {
			receivedFirstMessage = true
			go func() {
				time.Sleep(5 * time.Second)
				flushOnce.Do(func() {
					flushLogToDisk(nodeNum)
				})
			}()
		}
	}
}

func generateKeys(nodeNum *int) {
	identityDir := "identities"
	if err := os.MkdirAll(identityDir, 0755); err != nil {
		log.Fatal(err)
	}

	for i := 0; i <= *nodeNum; i++ {
		var priv crypto.PrivKey
		var err error
		path := filepath.Join(identityDir, fmt.Sprintf("node%d.key", i))

		if _, err := os.Stat(path); err == nil {
			priv, err = loadOrCreateIdentity(path)
			if err != nil {
				log.Fatal(err)
			}
		} else {
			priv, _, err = crypto.GenerateEd25519Key(rand.Reader)
			if err != nil {
				log.Fatal(err)
			}
		}

		peerID, err := peer.IDFromPrivateKey(priv)
		if err != nil {
			log.Fatal(err)
		}

		data, err := crypto.MarshalPrivateKey(priv)
		if err != nil {
			log.Fatal(err)
		}
		if err := ioutil.WriteFile(path, data, 0644); err != nil {
			log.Fatal(err)
		}

		fmt.Printf("Node %d peer ID: %s\n", i, peerID)
	}
}

func loadOrCreateIdentity(path string) (crypto.PrivKey, error) {
	if data, err := ioutil.ReadFile(path); err == nil {
		return crypto.UnmarshalPrivateKey(data)
	}

	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, err
	}
	data, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if err := ioutil.WriteFile(path, data, 0644); err != nil {
		return nil, err
	}
	return priv, nil
}

func main() {
	port := flag.Int("port", 0, "Port to listen on")
	peers := flag.String("peers", "", "Comma-separated list of peer addresses to connect to")
	nodeNum := flag.Int("node", 0, "Node number")
	minNum := flag.Int("minnode", 0, "Min node number")
	generate := flag.Bool("generate", false, "Generate new keys and print peer IDs")
	flag.Parse()

	if *generate {
		generateKeys(nodeNum)
		return
	}

	identityDir := "identities"
	if err := os.MkdirAll(identityDir, 0755); err != nil {
		log.Fatal(err)
	}

	identityFile := filepath.Join(identityDir, fmt.Sprintf("node%d.key", *nodeNum))
	privKey, err := loadOrCreateIdentity(identityFile)
	if err != nil {
		log.Fatal(err)
	}

	h, err := libp2p.New(
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", *port)),
		libp2p.Identity(privKey),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer h.Close()

	logWithTime("Node %d ID: %s\n", *nodeNum, h.ID())
	for _, addr := range h.Addrs() {
		fullAddr := fmt.Sprintf("%s/p2p/%s", addr, h.ID())
		logWithTime("Node %d Full address: %s\n", *nodeNum, fullAddr)
	}

	ps, err := pubsub.NewGossipSub(context.Background(), h, pubsub.HIERARCHICAL_GOSSIP)
	if err != nil {
		log.Fatal(err)
	}

	topic, err := ps.Join(topicName)
	if err != nil {
		log.Fatal(err)
	}
	defer topic.Close()

	sub, err := topic.Subscribe()
	if err != nil {
		log.Fatal(err)
	}
	defer sub.Cancel()

	go handleMessages(sub, *nodeNum)

	if *peers != "" {
		time.Sleep(1 * time.Second) // Let the network stabilize
		for _, addr := range strings.Split(*peers, ",") {
			addr = strings.TrimSpace(addr)
			if addr == "" {
				continue
			}
			maddr, err := multiaddr.NewMultiaddr(addr)
			if err != nil {
				logWithTime("Error parsing peer address %s: %v\n", addr, err)
				continue
			}
			peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
			if err != nil {
				logWithTime("Error extracting peer info from %s: %v\n", addr, err)
				continue
			}
			if err := h.Connect(context.Background(), *peerInfo); err != nil {
				logWithTime("Error connecting to peer %s: %v\n", addr, err)
				// Start retry goroutine for failed connections
				go func(peerInfo peer.AddrInfo, addr string) {
					for i := 0; i < 3; i++ {
						baseBackoff := time.Duration(i+1) * 5 * time.Second
						// Add jitter: random delay between 50% and 150% of base backoff
						jitter := time.Duration(mathrand.Intn(int(baseBackoff))) + baseBackoff/2
						time.Sleep(jitter)
						if err := h.Connect(context.Background(), peerInfo); err == nil {
							logWithTime("Node %d successfully reconnected to peer: %s\n", *nodeNum, peerInfo.ID)
							return
						} else {
							logWithTime("Node %d retry %d failed for peer %s: %v\n", *nodeNum, i+1, peerInfo.ID, err)
						}
					}
					logWithTime("Node %d failed to connect to peer %s after 5 retries\n", *nodeNum, peerInfo.ID)
				}(*peerInfo, addr)
				continue
			}
			logWithTime("Node %d connected to peer: %s\n", *nodeNum, peerInfo.ID)
		}
	}

	if *port == 4000+*minNum {
		time.Sleep(30 * time.Second)
		err = topic.Publish(context.Background(), []byte("Hello from GossipSub!"))
		if err != nil {
			log.Fatal(err)
		}
		logWithTime("Node %d published message to topic\n", *nodeNum)
	}

	// Handle shutdown
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	logWithTime("Node %d received shutdown signal\n", *nodeNum)

	flushOnce.Do(func() {
		flushLogToDisk(*nodeNum)
	})
}
