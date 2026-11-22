// Command r2-emulator serves the R2 emulator (emulators/r2) for running the
// service locally against it (README.md, Running it locally):
//
//	go run ./cmd/r2-emulator -addr 127.0.0.1:9000 \
//	  -token 'read-id:read-secret:read:lanescape-browser' \
//	  -object 'lanescape-browser/v1/latest=./latest'
//
// -token is <access key ID>:<secret access key>:<read|readwrite>:<bucket>[,<bucket>...],
// and -object, <bucket>/<key>=<file>, stores the file's contents. Both may
// be repeated; each bucket named is made. It holds everything in memory, and
// is for tests and local runs only.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	r2emu "github.com/lanescape/downloads/emulators/r2"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, " ") }
func (l *list) Set(s string) error { *l = append(*l, s); return nil }

func main() {
	addr := flag.String("addr", "127.0.0.1:9000", "where to listen")
	var tokens, objects list
	flag.Var(&tokens, "token", "an API token, <access key ID>:<secret access key>:<read|readwrite>:<bucket>[,<bucket>...]")
	flag.Var(&objects, "object", "an object to store, <bucket>/<key>=<file>")
	flag.Parse()
	e, err := build(tokens, objects)
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("r2-emulator listening on http://%s\n", ln.Addr())
	srv := &http.Server{Handler: e, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.Serve(ln))
}

func build(tokens, objects []string) (*r2emu.Emulator, error) {
	e := r2emu.New()
	// Errors name a -token by its position, never by anything in it.
	for i, t := range tokens {
		parts := strings.SplitN(t, ":", 4)
		if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[3] == "" {
			return nil, fmt.Errorf("-token %d: want <access key ID>:<secret access key>:<read|readwrite>:<buckets>", i+1)
		}
		var access r2emu.Access
		switch parts[2] {
		case "read":
			access = r2emu.Read
		case "readwrite":
			access = r2emu.ReadWrite
		default:
			return nil, fmt.Errorf("-token %d: the access isn't read or readwrite", i+1)
		}
		buckets := strings.Split(parts[3], ",")
		for _, b := range buckets {
			e.AddBucket(b)
		}
		e.AddToken(parts[0], parts[1], access, buckets...)
	}
	for _, o := range objects {
		where, file, ok := strings.Cut(o, "=")
		bucket, key, ok2 := strings.Cut(where, "/")
		if !ok || !ok2 || bucket == "" || key == "" || file == "" {
			return nil, errors.New("-object: want <bucket>/<key>=<file>")
		}
		body, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		e.Put(bucket, key, body, "")
	}
	return e, nil
}
