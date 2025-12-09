package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/sanke08/Distributed-Cache/internal/cache"
)

func (s *Server) acceptLoop() {
	for {
		conn, err := s.tcpLn.Accept()
		if err != nil {
			select {
			case <-s.shutdownCh:
				// expect shuting down
			default:
				log.Printf("[tcp] accept error: %v", err)
				continue
			}
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(conn)
		}()
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)

	var authUser string

	write := func(format string, a ...interface{}) {
		fmt.Fprintf(w, format+"\n", a...)
		w.Flush()
	}

	writeErr := func(msg string) {
		write("ERR %s", msg)
	}

	// set a generous deadline to read first command (we'll set per-command deadlines below)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))

	for {
		select {
		case <-s.shutdownCh:
			write("ERR server shutting down")
			return
		default:
		}

		// read line
		line, err := r.ReadString('\n')

		if err != nil {
			if err == io.EOF {
				return
			}
			writeErr("read error")
			return
		}

		// trims spaces
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// splits a string on any whitespace (spaces, tabs, etc.), and collapses multiple spaces.
		toks := strings.Fields(line)
		if len(toks) == 0 {
			writeErr("empty command")
			continue
		}

		cmd := strings.ToUpper(toks[0])

		// Per-command context with timeout
		_, cancel := context.WithTimeout(context.Background(), s.cfg.CmdTimeout)
		// ensure we cancel
		defer cancel()

		switch cmd {
		case "AUTH":
			if len(toks) != 2 {
				writeErr("usage: Auth <userID>")
				continue
			}
			authUser = toks[1]
			write("ok")

		case "PING":
			write("PONG")

		case "QUIT":
			write("BYE")
			return

		case "CREATEUSER":
			if len(toks) != 2 {
				writeErr("usage: CREATEUSER <userID>")
				continue
			}
			uid := toks[1]
			if err := s.cache.CreateUser(uid); err != nil {
				if err == cache.ErrUserExists {
					writeErr("user exists")
				} else {
					writeErr("internal")
				}
			} else {
				write("OK")
			}

		case "DELETEUSER":
			if len(toks) != 2 {
				writeErr("usage: DELETEUSER <userID>")
				continue
			}
			uid := toks[1]
			if err := s.cache.DeleteUser(uid); err != nil {
				if err == cache.ErrUserNotFound {
					writeErr("user not found")
				} else {
					writeErr("internal")
				}
			} else {
				write("OK")
			}

		case "SET":
			// forms:
			// 1) SET <key> <value> [ttl]  (requires AUTH)
			// 2) SET <user> <key> <value> [ttl]

			var uid, key, value string
			var ttlSec int64

			if len(toks) < 3 {
				writeErr("usage: SET <key> <value> [ttl] or SET <user> <key> <value> [ttl]")
				continue
			}
			if authUser != "" {
				// AUTH mode: tokens[1]=key, tokens[2]=value
				uid = authUser
				key = toks[1]
				value = toks[2]
				if len(toks) >= 4 {
					ttlSec, _ = strconv.ParseInt(toks[3], 10, 64)
				}
			} else {
				// user in command
				if len(toks) < 4 {
					writeErr("no auth and missing user in command")
					continue
				}
				uid = toks[1]
				key = toks[2]
				value = toks[3]
				if len(toks) >= 5 {
					ttlSec, _ = strconv.ParseInt(toks[4], 10, 64)
				}
			}
			ttl := time.Duration(0)
			if ttlSec > 0 {
				ttl = time.Duration(ttlSec) * time.Second
			}

			if err := s.cache.Set(uid, key, []byte(value), ttl); err != nil {
				if err == cache.ErrUserNotFound {
					writeErr("user not found")
				} else {
					writeErr("internal")
				}
			} else {
				write("OK")

			}

		case "GET":
			var uid, key string
			if authUser != "" {
				if len(toks) != 2 {
					writeErr("usage: GET <key>")
					continue
				}
				uid = authUser
				key = toks[1]
			} else {
				if len(toks) != 3 {
					writeErr("usage: GET <user> <key>")
					continue
				}
				uid = toks[1]
				key = toks[2]
			}

			val, err := s.cache.Get(uid, key)
			if err != nil {
				if err == cache.ErrUserNotFound || err == cache.ErrKeyNotFound {
					writeErr(err.Error())
				} else {
					writeErr("internal")
				}
			} else {
				write("VALUE %s", string(val))
			}

		case "DELETE":
			// same forms
			var uid, key string
			if authUser != "" {
				if len(toks) != 2 {
					writeErr("usage: DEL <key>")
					continue
				}
				uid = authUser
				key = toks[1]
			} else {
				if len(toks) != 3 {
					writeErr("usage: DEL <user> <key>")
					continue
				}
				uid = toks[1]
				key = toks[2]
			}

			if err := s.cache.Delete(uid, key); err != nil {
				if err == cache.ErrUserNotFound {
					writeErr("user not found")
				} else {
					writeErr("internal")
				}
			} else {
				write("OK")
			}

		case "KEYS":
			// KEYS (auth) or KEYS <user>
			var uid string
			if authUser != "" {
				uid = authUser
			} else {
				if len(toks) != 2 {
					writeErr("usage: KEYS <user>")
					continue
				}
				uid = toks[1]
			}
			keys, err := s.cache.ListKeys(uid)
			if err != nil {
				if err == cache.ErrUserNotFound {
					writeErr("user not found")
				} else {
					writeErr("internal")
				}
			} else {
				write("KEYS %s", strings.Join(keys, ","))
			}

		case "SNAPSHOT":
			// SNAPSHOT <userID>  or SNAPSHOT (with AUTH)
			var uid string
			if authUser != "" {
				uid = authUser
			} else if len(toks) == 2 {
				uid = toks[1]
			} else {
				writeErr("usage: SNAPSHOT <user>")
				continue
			}
			snap, err := s.cache.SnapshotUser(uid)

			if err != nil {
				if err == cache.ErrUserNotFound {
					writeErr("user not found")
				} else {
					writeErr("internal")
				}
				cancel()
				continue
			}
			if _, err := s.cache.SaveUserToFile(snap); err != nil {
				writeErr("save failed")
			} else {
				write("OK")
			}

		case "RESTORE":
			// RESTORE <userID>
			var uid string
			if authUser != "" && len(toks) == 1 {
				uid = authUser
			} else if len(toks) == 2 {
				uid = toks[1]
			} else {
				writeErr("usage: RESTORE <userID> or AUTH + RESTORE")
				cancel()
				continue
			}
			snap, err := s.cache.LoadUserFromFile(uid)
			if err != nil {
				writeErr("snapshot not found")
				cancel()
				continue
			}
			if err := s.cache.RestoreUserFromSnapshot(snap); err != nil {
				writeErr("restore failed")
			} else {
				write("OK")
			}

		default:
			writeErr("unknown command")
		}

		cancel()
	}
}
