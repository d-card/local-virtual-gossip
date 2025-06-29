package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const topicName = "gossipsub-test"

// Helper function to print with timestamp
func logWithTime(format string, a ...interface{}) {
	timestamp := time.Now().Format(time.RFC3339Nano)
	fmt.Printf("[%s] ", timestamp)
	fmt.Printf(format, a...)
}

func generateKeys(nodeNum *int) {
	// Create identity directory
	identityDir := "identities"
	if err := os.MkdirAll(identityDir, 0755); err != nil {
		log.Fatal(err)
	}

	// Generate keys for each node
	for i := 0; i <= *nodeNum; i++ {
		var priv crypto.PrivKey
		var err error
		if _, err := os.Stat(filepath.Join(identityDir, fmt.Sprintf("node%d.key", i))); err == nil {
			priv, err = loadOrCreateIdentity(filepath.Join(identityDir, fmt.Sprintf("node%d.key", i)))
			if err != nil {
				log.Fatal(err)
			}
		} else {
		// Generate new identity
			priv, _, err = crypto.GenerateEd25519Key(rand.Reader)
			if err != nil {
				log.Fatal(err)
			}
		}

		// Get the peer ID
		peerID, err := peer.IDFromPrivateKey(priv)
		if err != nil {
			log.Fatal(err)
		}

		// Save the identity
		data, err := crypto.MarshalPrivateKey(priv)
		if err != nil {
			log.Fatal(err)
		}

		identityFile := filepath.Join(identityDir, fmt.Sprintf("node%d.key", i))
		if err := ioutil.WriteFile(identityFile, data, 0644); err != nil {
			log.Fatal(err)
		}

		// Print the peer ID for use in the topology script
		fmt.Printf("Node %d peer ID: %s\n", i, peerID)
	}
}

func loadOrCreateIdentity(path string) (crypto.PrivKey, error) {
	// Try to load existing identity
	if data, err := ioutil.ReadFile(path); err == nil {
		return crypto.UnmarshalPrivateKey(data)
	}

	// Create new identity
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, err
	}

	// Save the identity
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
	// Parse command line flags
	port := flag.Int("port", 0, "Port to listen on")
	peers := flag.String("peers", "", "Comma-separated list of peer addresses to connect to")
	nodeNum := flag.Int("node", 0, "Node number")
	minNum := flag.Int("minnode", 0, "Min node number")
	generate := flag.Bool("generate", false, "Generate new keys and print peer IDs")
	flag.Parse()

	// If generate flag is set, generate necessary keys and exit
	if *generate {
		generateKeys(nodeNum)
		return
	}

	// Create identity directory
	identityDir := "identities"
	if err := os.MkdirAll(identityDir, 0755); err != nil {
		log.Fatal(err)
	}

	// Load or create identity
	identityFile := filepath.Join(identityDir, fmt.Sprintf("node%d.key", *nodeNum))
	privKey, err := loadOrCreateIdentity(identityFile)
	if err != nil {
		log.Fatal(err)
	}

	// Create a new libp2p host with the persistent identity
	h, err := libp2p.New(
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", *port)),
		libp2p.Identity(privKey),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer h.Close()

	// Print the node's peer ID and addresses
	logWithTime("Node %d ID: %s\n", *nodeNum, h.ID())

	// Print the full multiaddr for this node
	for _, addr := range h.Addrs() {
		fullAddr := fmt.Sprintf("%s/p2p/%s", addr, h.ID())
		logWithTime("Node %d Full address: %s\n", *nodeNum, fullAddr)
	}

	// Create a new GossipSub instance
	ps, err := pubsub.NewGossipSub(context.Background(), h, pubsub.HIERARCHICAL_GOSSIP)
	if err != nil {
		log.Fatal(err)
	}

	// Join the topic
	topic, err := ps.Join(topicName)
	if err != nil {
		log.Fatal(err)
	}
	defer topic.Close()

	// Subscribe to the topic
	sub, err := topic.Subscribe()
	if err != nil {
		log.Fatal(err)
	}
	defer sub.Cancel()

	// Start a goroutine to handle incoming messages
	go handleMessages(sub)

	// Connect to peers if specified
	if *peers != "" {
		peerAddrs := strings.Split(*peers, ",")
		time.Sleep(1 * time.Second)
		for _, addr := range peerAddrs {
			addr = strings.TrimSpace(addr)
			if addr == "" {
				continue
			}

			// Parse the multiaddr
			maddr, err := multiaddr.NewMultiaddr(addr)
			if err != nil {
				log.Printf("Error parsing peer address %s: %v", addr, err)
				continue
			}

			// Extract the peer ID from the multiaddr
			peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
			if err != nil {
				log.Printf("Error extracting peer info from %s: %v", addr, err)
				continue
			}

			// Connect to the peer
			if err := h.Connect(context.Background(), *peerInfo); err != nil {
				log.Printf("Error connecting to peer %s: %v", addr, err)
				continue
			}
			logWithTime("Node %d connected to peer: %s\n", *nodeNum, peerInfo.ID)
		}
	}

	// If this is the first node, publish a message

	if *port == 4000 + *minNum {
		time.Sleep(30 * time.Second) // Wait for other nodes to connect
		err = topic.Publish(context.Background(), []byte("Hello from GossipSub!"))
		if err != nil {
			log.Fatal(err)
		}
		logWithTime("Node %d published message to topic\n", *nodeNum)
	}

	// Wait for a SIGINT or SIGTERM signal
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	logWithTime("Node %d received signal, shutting down...\n", *nodeNum)
}

func handleMessages(sub *pubsub.Subscription) {
	for {
		msg, err := sub.Next(context.Background())
		if err != nil {
			log.Fatal(err)
		}
		logWithTime("Received message from %s: %s\n", msg.ReceivedFrom, string(msg.Data))
	}
} 