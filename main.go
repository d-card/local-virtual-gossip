package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const topicName = "gossipsub-test"

func logWithTime(format string, a ...interface{}) {
	timestamp := time.Now().Format(time.RFC3339Nano)
	line := fmt.Sprintf("[%s] %s", timestamp, fmt.Sprintf(format, a...))
	fmt.Print(line) 
}



func handleMessages(sub *pubsub.Subscription, nodeNum int) {
	for {
		msg, err := sub.Next(context.Background())
		if err != nil {
			log.Fatal(err)
		}
		logWithTime("Received message from %s: %s\n", msg.ReceivedFrom, string(msg.Data))
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

		priv, err = loadOrCreateIdentity(path)

		peerID, err := peer.IDFromPrivateKey(priv)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf("%d:%s\n", i, peerID)
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

	ps, err := pubsub.NewGossipSub(context.Background(), h, pubsub.GOSSIPSUB)
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
				continue
			}
			logWithTime("Node %d connected to peer: %s\n", *nodeNum, peerInfo.ID)
		}
	}

	if *port == 4000+*minNum {
		time.Sleep(60 * time.Second)
		err = topic.Publish(context.Background(), []byte("Hello world!"))
		if err != nil {
			log.Fatal(err)
		}
		logWithTime("Node %d published message to topic\n", *nodeNum)
		time.Sleep(5 * time.Second) // Allow time for message to propagate
		logWithTime("Node %d shutting down\n", *nodeNum)
		os.Exit(0)
	}


	// Wait for all messages to be processed before shutting down
	time.Sleep(120 * time.Second)
	logWithTime("Node %d shutting down\n", *nodeNum)
	os.Exit(0)
}
