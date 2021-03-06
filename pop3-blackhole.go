package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"runtime"
	"strings"
	"time"
)

type config struct {
	latency  time.Duration
	verbose  bool
	servetls bool
	tls      tls.Config
}

type handler struct {
	s string
	f func(*net.Conn, []byte, *config)
}

var responses = map[string]handler{
	"CAPA": {"+OK Capability list follows\r\nSASL PLAIN\r\n.\r\n", nil},
	"AUTH": {"", handleAuth},
	"STAT": {"+OK 0 0\r\n", nil},
	"USER": {"+OK Password required\r\n", nil},
}

func sendResponse(c *net.Conn, s string, verbose bool) {
	(*c).Write([]byte(s))
	if verbose {
		strs := strings.Split(s, "\r\n")
		for _, str := range strs {
			if len(str) != 0 {
				log.Printf("<- [%s]", str)
			}
		}
	}
}

func handleConnection(c *net.Conn, conf *config) {
	fmt.Println("\nNew connection from", (*c).RemoteAddr().String())

	// Print banner
	sendResponse(c, "+OK POP3 PROXY server ready blackhole.smtp.localhost\r\n", conf.verbose)

	// Handle commands
	for {
		// Read command
		readBuf := make([]byte, 4096)
		l, e := (*c).Read(readBuf)
		if e != nil {
			_ = (*c).Close()
			return
		}

		// Log command
		if conf.verbose {
			log.Printf("-> [%s]", strings.Trim(string(readBuf[0:l]), "\r\n "))
		}

		// Add latency
		if conf.latency != 0 {
			time.Sleep(conf.latency * time.Millisecond)
		}

		// Send response
		h, ok := responses[strings.ToUpper(string(readBuf[0:4]))]
		if ok {
			sendResponse(c, h.s, conf.verbose)
			if h.f != nil {
				// Run callback to handle transaction
				h.f(c, readBuf, conf)
			}
		} else {
			sendResponse(c, "+OK\r\n", conf.verbose)
		}
	}
}

func handleAuth(c *net.Conn, b []byte, conf *config) {
	authLine := strings.TrimSpace(strings.Trim(string(b), "\r\n \t\000"))

	if authLine == "AUTH" {
		sendResponse(c, "+OK Maildrop locked and ready\r\n", conf.verbose)
	} else if authLine == "AUTH PLAIN" {
		sendResponse(c, "+\r\n", conf.verbose)

		// Read data
		l, e := (*c).Read(b)
		if e != nil || l == 0 {
			fmt.Println("Couldn't read additional info for AUTH PLAIN")
			return
		}

		authLine = strings.TrimSpace(strings.Trim(string(b), "\r\n \t\000"))
		if conf.verbose {
			log.Printf("-> [%s]", authLine)
		}

		sendResponse(c, "+OK Maildrop locked and ready\r\n", conf.verbose)
	} else {
		sendResponse(c, "+OK Maildrop locked and ready\r\n", conf.verbose)
	}
}

func handleStarttls(c *net.Conn, b []byte, conf *config) {
	*c = tls.Server(*c, &conf.tls)
}

func main() {
	var conf config
	var port, latency, cpus int
	var certFile, keyFile string

	flag.StringVar(&certFile, "cert", "", "Certficate file (PEM encoded)")
	flag.IntVar(&cpus, "cpus", 2, "Number of CPUs/kernel threads used")
	flag.StringVar(&keyFile, "key", "", "Private key file (PEM encoded)")
	flag.IntVar(&latency, "latency", 0, "Latency in milliseconds")
	flag.IntVar(&port, "port", 25, "TCP port")
	flag.BoolVar(&conf.verbose, "verbose", false, "Show the POP3 traffic")
	flag.BoolVar(&conf.servetls, "tls", false, "Serve TLS on the selected port (e.g. 995)")

	flag.Parse()

	// Use cpus kernel threads
	runtime.GOMAXPROCS(cpus)

	// Set latency
	if latency < 0 || 1000000 < latency {
		latency = 0
	}
	conf.latency = time.Duration(latency) * time.Millisecond

	if certFile != "" {
		fmt.Println("Loading TLS certs")
		// Load certificate
		if keyFile == "" {
			// Assume the private key is in the same file as the certificate
			keyFile = certFile
		}
		cert, e := tls.LoadX509KeyPair(certFile, keyFile)
		if e != nil {
			// Error!
			log.Panic(e)
			return
		}
		conf.tls.Certificates = []tls.Certificate{cert}
	}

	// Get address:port
	a, e := net.ResolveTCPAddr("tcp4", fmt.Sprintf(":%d", port))
	if e != nil {
		// Error!
		log.Panic(e)
		return
	}

	// Start listening for incoming connections
	var l net.Listener
	if conf.servetls {
		l, e = tls.Listen("tcp", fmt.Sprintf(":%d", port), &conf.tls)
	} else {
		l, e = net.ListenTCP("tcp", a)
	}

	if e != nil {
		// Error!
		log.Panic(e)
		return
	}

	// Accept connections then handle each one in a dedicated goroutine
	for {
		c, e := l.Accept()
		if e != nil {
			continue
		}
		go handleConnection(&c, &conf)
	}
}
