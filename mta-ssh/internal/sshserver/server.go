package sshserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/imjasonh/terraform-playground/mta-ssh/internal/display"
	"github.com/imjasonh/terraform-playground/mta-ssh/internal/mta"
	"golang.org/x/crypto/ssh"
)

type Server struct {
	Addr   string
	Client *mta.Client

	hostKey ssh.Signer
}

func New(addr string, client *mta.Client) (*Server, error) {
	key, err := loadOrGenerateHostKey()
	if err != nil {
		return nil, err
	}
	return &Server{
		Addr:    addr,
		Client:  client,
		hostKey: key,
	}, nil
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	config := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	config.AddHostKey(s.hostKey)

	listener, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.Addr, err)
	}
	defer listener.Close()

	log.Printf("mta-ssh listening on %s", s.Addr)

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("accept: %w", err)
			}
		}
		go s.handleConn(conn, config)
	}
}

func (s *Server) handleConn(tcpConn net.Conn, config *ssh.ServerConfig) {
	sshConn, chans, reqs, err := ssh.NewServerConn(tcpConn, config)
	if err != nil {
		log.Printf("handshake failed: %v", err)
		return
	}
	defer sshConn.Close()

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			log.Printf("channel accept: %v", err)
			continue
		}
		go s.handleSession(channel, requests)
	}
}

func (s *Server) handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()

	var (
		ptyReq bool
		term   = "xterm-256color"
		width  = 100
		height = 40
	)

	for req := range requests {
		switch req.Type {
		case "pty-req":
			ptyReq = true
			if len(req.Payload) >= 8 {
				termLen := int(req.Payload[4])<<24 | int(req.Payload[5])<<16 | int(req.Payload[6])<<8 | int(req.Payload[7])
				if len(req.Payload) >= 8+termLen+8 {
					term = string(req.Payload[8 : 8+termLen])
					off := 8 + termLen
					width = int(req.Payload[off])<<24 | int(req.Payload[off+1])<<16 | int(req.Payload[off+2])<<8 | int(req.Payload[off+3])
					height = int(req.Payload[off+4])<<24 | int(req.Payload[off+5])<<16 | int(req.Payload[off+6])<<8 | int(req.Payload[off+7])
				}
			}
			_ = req.Reply(true, nil)
		case "window-change":
			if len(req.Payload) >= 8 {
				width = int(req.Payload[0])<<24 | int(req.Payload[1])<<16 | int(req.Payload[2])<<8 | int(req.Payload[3])
				height = int(req.Payload[4])<<24 | int(req.Payload[5])<<16 | int(req.Payload[6])<<8 | int(req.Payload[7])
			}
			_ = req.Reply(true, nil)
		case "shell":
			if !ptyReq {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			s.runLiveDisplay(channel, term, width, height)
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func (s *Server) runLiveDisplay(out io.Writer, term string, width, height int) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	var writeMu sync.Mutex
	render := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		feed, err := s.Client.Fetch(ctx)
		if err != nil {
			msg := fmt.Sprintf("\x1b[2J\x1b[H\x1b[31mError fetching MTA feed: %v\x1b[0m\r\n", err)
			writeMu.Lock()
			_, _ = io.WriteString(out, msg)
			writeMu.Unlock()
			return
		}

		screen := display.Render(feed, time.Now(), width)
		writeMu.Lock()
		_, _ = io.WriteString(out, screen)
		writeMu.Unlock()
	}

	render()
	for range ticker.C {
		render()
	}
}

func loadOrGenerateHostKey() (ssh.Signer, error) {
	path := os.Getenv("SSH_HOST_KEY")
	if path == "" {
		path = "host_key"
	}

	if data, err := os.ReadFile(path); err == nil {
		signer, err := ssh.ParsePrivateKey(data)
		if err == nil {
			return signer, nil
		}
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	pemBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(pemBlock), 0o600); err != nil {
		log.Printf("warning: could not persist host key to %s: %v", path, err)
	}

	return ssh.NewSignerFromKey(privateKey)
}
