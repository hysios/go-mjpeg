// +build ignore

package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"github.com/hysios/go-mjpeg"

	"gocv.io/x/gocv"
)

var (
	camera   = flag.String("camera", "0", "Camera ID")
	addr     = flag.String("addr", ":8080", "Server address")
	xml      = flag.String("classifier", "haarcascade_frontalface_default.xml", "classifier XML file")
	interval = flag.Duration("interval", 10*time.Millisecond, "interval")
)

func capture(ctx context.Context, wg *sync.WaitGroup, stream *mjpeg.Stream, wsstream *mjpeg.WSStream) {
	defer wg.Done()

	var webcam *gocv.VideoCapture
	var err error
	if id, err := strconv.ParseInt(*camera, 10, 64); err == nil {
		webcam, err = gocv.VideoCaptureDevice(int(id))
	} else {
		webcam, err = gocv.VideoCaptureFile(*camera)
	}
	if err != nil {
		log.Println("unable to init web cam: %v", err)
		return
	}
	defer webcam.Close()

	im := gocv.NewMat()

	for len(ctx.Done()) == 0 {
		var (
			buf     []byte
			sn, wsn bool
		)

		sn, wsn = stream.NWatch() > 0, wsstream.NWatch() > 0
		if sn || wsn {
			if ok := webcam.Read(&im); !ok {
				continue
			}

			buf, err = gocv.IMEncode(".jpg", im)
			if err != nil {
				continue
			}
		}
		if sn {
			err = stream.Update(buf)
			if err != nil {
				break
			}
		}
		if wsn {
			err = wsstream.Update(buf)
			if err != nil {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func main() {
	flag.Parse()

	stream := mjpeg.NewStreamWithInterval(*interval)
	wsstream := mjpeg.NewWSStreamWithInterval(*interval)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go capture(ctx, &wg, stream, wsstream)

	http.HandleFunc("/mjpeg", stream.ServeHTTP)
	http.HandleFunc("/stream", wsstream.ServeHTTP)

	// http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
	// 	w.Header().Set("Content-Type", "text/html")
	// 	w.Write([]byte(`<img src="/mjpeg" />`))
	// })
	http.Handle("/", http.StripPrefix("/", http.FileServer(http.Dir("_example"))))

	server := &http.Server{Addr: *addr}
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, os.Interrupt)
	go func() {
		<-sc
		server.Shutdown(ctx)
	}()
	server.ListenAndServe()
	stream.Close()
	cancel()

	wg.Wait()
}
