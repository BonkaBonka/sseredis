package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis"
)

type Response map[string]interface{}

type universalHandler struct {
	client          *redis.Client
	prefix          string
	keepAliveTime   time.Duration
	clientRetryTime string
}

func (handler *universalHandler) subscriber(res http.ResponseWriter, req *http.Request) {
	flusher, ok := res.(http.Flusher)
	if !ok {
		http.Error(res, "Streaming Unsupported", http.StatusInternalServerError)
		return
	}

	queue := path.Base(req.URL.Path)
	pubsub := handler.client.Subscribe(queue)
	defer pubsub.Close()

	channel := pubsub.Channel()

	res.Header().Set("Cache-Control", "no-cache")
	res.Header().Set("Connection", "keep-alive")
	res.Header().Set("Transfer-Encoding", "chunked")
	res.Header().Set("X-Accel-Buffering", "no")
	res.Header().Set("Content-Type", "text/event-stream")
	res.WriteHeader(http.StatusOK)

	_, err := res.Write([]byte(": --->" + strings.Repeat(" ", 2048) + "<--- padding\n\n"))
	if err != nil {
		msg := "Padding Transmit Failed: " + err.Error()
		log.Print(msg)
		return
	}

	if handler.clientRetryTime != "" {
		_, err = res.Write([]byte("retry: " + handler.clientRetryTime + "\n\n"))
		if err != nil {
			msg := "Retry-time Transmit Failed: " + err.Error()
			log.Print(msg)
			return
		}
	}

	for {
		flusher.Flush()

		var timeout <-chan time.Time
		if handler.keepAliveTime > 0 {
			timeout = time.After(handler.keepAliveTime * time.Second)
		}
		select {
		case msg := <-channel:
			if msg.Payload != "" {
				_, err = res.Write([]byte("event: " + msg.Channel + "\n"))
				if err != nil {
					msg := "Event Name Transmit Failed: " + err.Error()
					log.Print(msg)
					return
				}

				messages := strings.Split(msg.Payload, "\n")
				for index := range messages {
					_, err := res.Write([]byte("data: " + messages[index] + "\n"))
					if err != nil {
						msg := "Message Transmit Failed: " + err.Error()
						log.Print(msg)
						return
					}
				}
				_, err := res.Write([]byte("\n"))
				if err != nil {
					msg := "Message Transmit Failed: " + err.Error()
					log.Print(msg)
					return
				}
			}
		case <-timeout:
			_, err := res.Write([]byte(": keep-alive\n\n"))
			if err != nil {
				return
			}
		}
	}
}

func (handler *universalHandler) publisher(res http.ResponseWriter, req *http.Request) {
	msg, err := ioutil.ReadAll(req.Body)
	if err != nil {
		msg := "Message Transmit Failed: " + err.Error()
		log.Print(msg)
		http.Error(res, msg, http.StatusInternalServerError)
		return
	}

	queue := path.Base(req.URL.Path)
	subscribers := handler.client.Publish(queue, string(msg)).Val()

	res.Header().Set("Cache-Control", "no-cache")

	if req.Header.Get("Accept") == "application/json" {
		res.Header().Set("Content-Type", "application/json")
		json.NewEncoder(res).Encode(Response{"success": true, "subscribers": subscribers})
	} else {
		res.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(res, "OK - Subscribers: %d", subscribers)
	}
}

func (handler *universalHandler) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	if path.Dir(req.URL.Path) != handler.prefix {
		msg := fmt.Sprint("Invalid path: ", req.URL.String())
		log.Print(msg)
		http.Error(res, msg, http.StatusInternalServerError)
		return
	}

	switch req.Method {
	case "GET":
		handler.subscriber(res, req)
	case "POST":
		handler.publisher(res, req)
	default:
		msg := fmt.Sprint("Invalid method: ", req.Method)
		log.Print(msg)
		http.Error(res, msg, http.StatusMethodNotAllowed)
	}
}

func main() {
	var redisAddr = flag.String("redis-addr", "localhost:6379", "redis address")
	var redisPass = flag.String("redis-pass", "", "redis password")
	var redisDb = flag.Int("redis-db", -1, "redis database number")
	var listenAddr = flag.String("listen-addr", "localhost:8080", "listen address")
	var urlPrefix = flag.String("url-prefix", "/redis", "URL prefix")
	var keepAlive = flag.Int("keepalive", 30, "seconds between keep-alive messages (0 to disable")
	var clientRetry = flag.Float64("client-retry", 0.0, "seconds for the client to wait before reconnecting (0 to use browser defaults)")

	flag.Parse()

	log.Print("Redis Address  : ", *redisAddr)
	log.Print("Redis Password : ", *redisPass)
	log.Print("Redis Database : ", *redisDb)
	log.Print("Listen Address : ", *listenAddr)
	log.Print("URL Prefix     : ", *urlPrefix)
	log.Print("Keep-Alive     : ", *keepAlive)

	var clientRetryTime string
	if *clientRetry > 0.0 {
		log.Print("Client Retry   : ", *clientRetry)
		clientRetryTime = strconv.Itoa(int(*clientRetry * 1000.0))
	}

	client := redis.NewClient(&redis.Options{
		Addr:     *redisAddr,
		Password: *redisPass,
		DB:       *redisDb,
	})
	defer client.Close()

	server := http.Server{
		Addr: *listenAddr,
		Handler: &universalHandler{
			client,
			*urlPrefix,
			time.Duration(*keepAlive),
			clientRetryTime,
		},
	}

	log.Printf("Listening on %s", server.Addr)

	log.Fatal(server.ListenAndServe())
}
