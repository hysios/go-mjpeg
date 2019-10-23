package mjpeg

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 10 * 1024 * 1024
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type WSStream struct {
	m        sync.Mutex
	s        map[chan []byte]struct{}
	Interval time.Duration
}

func NewWSStream() *WSStream {
	return &WSStream{
		s: make(map[chan []byte]struct{}),
	}
}

func NewWSStreamWithInterval(interval time.Duration) *WSStream {
	return &WSStream{
		s:        make(map[chan []byte]struct{}),
		Interval: interval,
	}
}

func (ws *WSStream) Close() error {
	ws.m.Lock()
	defer ws.m.Unlock()
	for c := range ws.s {
		close(c)
		delete(ws.s, c)
	}
	ws.s = nil
	return nil
}

func (ws *WSStream) Update(b []byte) error {
	ws.m.Lock()
	defer ws.m.Unlock()
	if ws.s == nil {
		return errors.New("stream was closed")
	}
	for c := range ws.s {
		select {
		case c <- b:
		default:
		}
	}
	return nil
}

func (ws *WSStream) add(c chan []byte) {
	ws.m.Lock()
	ws.s[c] = struct{}{}
	ws.m.Unlock()
}

func (ws *WSStream) destroy(c chan []byte) {
	ws.m.Lock()
	if ws.s != nil {
		close(c)
		delete(ws.s, c)
	}
	ws.m.Unlock()
}

func (ws *WSStream) NWatch() int {
	return len(ws.s)
}

func (ws *WSStream) Current() []byte {
	c := make(chan []byte)
	ws.add(c)
	defer ws.destroy(c)

	return <-c
}

func (ws *WSStream) readPump(conn *websocket.Conn) {
	defer func() {
		conn.Close()
	}()

	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error { conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}
		message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
	}
}

func (ws *WSStream) writePump(c chan []byte, conn *websocket.Conn) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		conn.Close()
	}()
	for {
		select {
		case message, ok := <-c:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued chat messages to the current websocket message.
			// n := len(c)
			// for i := 0; i < n; i++ {
			// 	w.Write(newline)
			// 	w.Write(<-c)
			// }

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (ws *WSStream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := make(chan []byte)
	ws.add(c)
	defer ws.destroy(c)

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	defer conn.Close()

	for {
		time.Sleep(ws.Interval)

		b, ok := <-c
		if !ok {
			break
		}

		conn.SetWriteDeadline(time.Now().Add(writeWait))
		if !ok {
			// The hub closed the channel.
			conn.WriteMessage(websocket.CloseMessage, []byte{})
			return
		}

		w, err := conn.NextWriter(websocket.BinaryMessage)
		if err != nil {
			return
		}
		w.Write(b)

		if err := w.Close(); err != nil {
			return
		}
	}
}
