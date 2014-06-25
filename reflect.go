// Copyright 2011 Miek Gieben. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Reflect is a small name server which sends back the IP address of its client, the
// recursive resolver.
// When queried for type A (resp. AAAA), it sends back the IPv4 (resp. v6) address.
// In the additional section the port number and transport are shown.
//
// Basic use pattern:
//
//  dig @localhost -p 8053 whoami.miek.nl A
//
//  ;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 2157
//  ;; flags: qr rd; QUERY: 1, ANSWER: 1, AUTHORITY: 0, ADDITIONAL: 1
//  ;; QUESTION SECTION:
//  ;whoami.miek.nl.            IN  A
//
//  ;; ANSWER SECTION:
//  whoami.miek.nl.     0   IN  A   127.0.0.1
//
//  ;; ADDITIONAL SECTION:
//  whoami.miek.nl.     0   IN  TXT "Port: 56195 (udp)"
//
// Similar services: whoami.ultradns.net, whoami.akamai.net. Also (but it
// is not their normal goal): rs.dns-oarc.net, porttest.dns-oarc.net,
// amiopen.openresolvers.org.
//
// Original version is from: Stephane Bortzmeyer <stephane+grong@bortzmeyer.org>.
//
// Adapted to Go (i.e. completely rewritten) by Miek Gieben <miek@miek.nl>.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	_ "github.com/mattn/go-sqlite3" // imported for its side-effect (providing a sqlite3 driver).
	"github.com/miekg/dns"
	"log"
	"net"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	printf   *bool
	compress *bool
	pool     *bool
	tsig     *string
)

const dom = "whoami.miek.nl."
const sqlite_file = "/tmp/dns.sqlite3"

func lookUpNameInDb(name, rrtype string) (ttl int, rdata string, err error) {
	// Theoretically we'd have different DBs for different zones, or whatever.
	db, err := sql.Open("sqlite3", sqlite_file)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		log.Fatal(err)
	}
	defer tx.Commit()
	stmt, err := tx.Prepare("select * from records where name = ? and type = ?")
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()
	rows, err := stmt.Query(name, rrtype)
	defer rows.Close()
	for rows.Next() {
		err = rows.Scan(&name, &rrtype, &ttl, &rdata)
	}
	return
}

var sqliteHandled = 0

func handleSqlite(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	q := r.Question
	n := q[0].Name

	var rr dns.RR
	switch r.Question[0].Qtype {

	case dns.TypeA:
		rr = new(dns.A)
		ttl, rdata, err := lookUpNameInDb(r.Question[0].Name, "A")
		if err != nil {
			log.Fatal(err)
		} else {
			// Obviously this is mad-hacky.
			var octets [4]byte
			stringOctets := strings.Split(rdata, ".")
			for i := range stringOctets {
				octetInt, _ := strconv.Atoi(stringOctets[i])
				octets[i] = byte(octetInt)
			}
			rr.(*dns.A).A = net.IPv4(octets[0], octets[1], octets[2], octets[3]).To4()
			rr.(*dns.A).Hdr = dns.RR_Header{
				Name:   n,
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    uint32(ttl),
			}
			m.Answer = append(m.Answer)
		}

	default:
		rr = new(dns.ANY)
		rr.(*dns.ANY).Hdr = dns.RR_Header{
			Name:   n,
			Rrtype: dns.TypeANY,
			Class:  dns.ClassINET,
			Ttl:    60,
		}

	}
	m.Answer = append(m.Answer, rr)
	w.WriteMsg(m)
}

var reflectHandled = 0

func handleReflect(w dns.ResponseWriter, r *dns.Msg) {
	reflectHandled += 1
	if reflectHandled%1000 == 0 {
		fmt.Printf("Served %d reflections\n", reflectHandled)
	}
	var (
		v4  bool
		rr  dns.RR
		str string
		a   net.IP
	)
	m := new(dns.Msg)
	m.SetReply(r)
	m.Compress = *compress
	if ip, ok := w.RemoteAddr().(*net.UDPAddr); ok {
		str = "Port: " + strconv.Itoa(ip.Port) + " (udp)"
		a = ip.IP
		v4 = a.To4() != nil
	}
	if ip, ok := w.RemoteAddr().(*net.TCPAddr); ok {
		str = "Port: " + strconv.Itoa(ip.Port) + " (tcp)"
		a = ip.IP
		v4 = a.To4() != nil
	}

	if v4 {
		rr = new(dns.A)
		rr.(*dns.A).Hdr = dns.RR_Header{Name: dom, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0}
		rr.(*dns.A).A = a.To4()
	} else {
		rr = new(dns.AAAA)
		rr.(*dns.AAAA).Hdr = dns.RR_Header{Name: dom, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 0}
		rr.(*dns.AAAA).AAAA = a
	}

	t := new(dns.TXT)
	t.Hdr = dns.RR_Header{Name: dom, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0}
	t.Txt = []string{str}

	switch r.Question[0].Qtype {
	case dns.TypeTXT:
		m.Answer = append(m.Answer, t)
		m.Extra = append(m.Extra, rr)
	default:
		fallthrough
	case dns.TypeAAAA, dns.TypeA:
		m.Answer = append(m.Answer, rr)
		m.Extra = append(m.Extra, t)

	case dns.TypeAXFR, dns.TypeIXFR:
		c := make(chan *dns.Envelope)
		tr := new(dns.Transfer)
		defer close(c)
		err := tr.Out(w, r, c)
		if err != nil {
			return
		}
		soa, _ := dns.NewRR(`whoami.miek.nl. 0 IN SOA linode.atoom.net. miek.miek.nl. 2009032802 21600 7200 604800 3600`)
		c <- &dns.Envelope{RR: []dns.RR{soa, t, rr, soa}}
		w.Hijack()
		// w.Close() // Client closes connection
		return

	}

	if r.IsTsig() != nil {
		if w.TsigStatus() == nil {
			m.SetTsig(r.Extra[len(r.Extra)-1].(*dns.TSIG).Hdr.Name, dns.HmacMD5, 300, time.Now().Unix())
		} else {
			println("Status", w.TsigStatus().Error())
		}
	}
	if *printf {
		fmt.Printf("%v\n", m.String())
	}
	// set TC when question is tc.miek.nl.
	if m.Question[0].Name == "tc.miek.nl." {
		m.Truncated = true
		// send half a message
		buf, _ := m.Pack()
		w.Write(buf[:len(buf)/2])
		return
	}
	w.WriteMsg(m)
}

func serve(net, name, secret string) {
	switch name {
	case "":
		server := &dns.Server{Pool: *pool, Addr: ":8053", Net: net, TsigSecret: nil}
		err := server.ListenAndServe()
		if err != nil {
			fmt.Printf("Failed to setup the "+net+" server: %s\n", err.Error())
		}
	default:
		server := &dns.Server{Pool: *pool, Addr: ":8053", Net: net, TsigSecret: map[string]string{name: secret}}
		err := server.ListenAndServe()
		if err != nil {
			fmt.Printf("Failed to setup the "+net+" server: %s\n", err.Error())
		}
	}
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU() * 4)
	cpuprofile := flag.String("cpuprofile", "", "write cpu profile to file")
	printf = flag.Bool("print", false, "print replies")
	compress = flag.Bool("compress", false, "compress replies")
	pool = flag.Bool("pool", false, "use UDP memory pooling")
	tsig = flag.String("tsig", "", "use MD5 hmac tsig: keyname:base64")
	var name, secret string
	flag.Usage = func() {
		flag.PrintDefaults()
	}
	flag.Parse()
	if *tsig != "" {
		a := strings.SplitN(*tsig, ":", 2)
		name, secret = dns.Fqdn(a[0]), a[1] // fqdn the name, which everybody forgets...
	}
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	dns.HandleFunc("miek.nl.", handleReflect)
	dns.HandleFunc("baz.miek.nl.", handleSqlite)

	go serve("tcp", name, secret)
	go serve("udp", name, secret)
	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
forever:
	for {
		select {
		case s := <-sig:
			fmt.Printf("Signal (%d) received, stopping\n", s)
			break forever
		}
	}
}
