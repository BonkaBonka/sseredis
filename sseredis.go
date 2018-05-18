package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis"
	"github.com/gorilla/mux"
)

type Response map[string]interface{}

var client *redis.Client
var keepAliveTime time.Duration
var clientRetryTime string

func subscriber(res http.ResponseWriter, req *http.Request) {
	flusher, ok := res.(http.Flusher)
	if !ok {
		http.Error(res, "Streaming Unsupported", http.StatusInternalServerError)
		return
	}

	vars := mux.Vars(req)
	queue := vars["queue"]

	pubsub := client.Subscribe(queue)
	defer pubsub.Close()

	channel := pubsub.Channel()

	res.Header().Set("Access-Control-Allow-Origin", "*")
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

	if clientRetryTime != "" {
		_, err = res.Write([]byte("retry: " + clientRetryTime + "\n\n"))
		if err != nil {
			msg := "Retry-time Transmit Failed: " + err.Error()
			log.Print(msg)
			return
		}
	}

	for {
		flusher.Flush()

		var timeout <-chan time.Time
		if keepAliveTime > 0 {
			timeout = time.After(keepAliveTime * time.Second)
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

func publisher(res http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	queue := vars["queue"]

	msg, err := ioutil.ReadAll(req.Body)
	if err != nil {
		msg := "Message Transmit Failed: " + err.Error()
		log.Print(msg)
		http.Error(res, msg, http.StatusInternalServerError)
		return
	}

	subscribers := client.Publish(queue, string(msg)).Val()

	res.Header().Set("Access-Control-Allow-Origin", "*")
	res.Header().Set("Cache-Control", "no-cache")

	if req.Header.Get("Accept") == "application/json" {
		res.Header().Set("Content-Type", "application/json")
		json.NewEncoder(res).Encode(Response{"success": true, "subscribers": subscribers})
	} else {
		res.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(res, "OK - Subscribers: %d", subscribers)
	}
}

func main() {
	var redisAddr = flag.String("redis-addr", "localhost:6379", "redis address")
	var redisPass = flag.String("redis-pass", "", "redis password")
	var redisDb = flag.Int("redis-db", -1, "redis database number")
	var urlPrefix = flag.String("url-prefix", "redis", "URL prefix")
	var listenAddr = flag.String("listen-addr", "localhost:8080", "listen address")
	var allowPosts = flag.Bool("allow-posts", false, "allow POSTing to the queue")
	var keepAlive = flag.Int("keepalive", 30, "seconds between keep-alive messages (0 to disable")
	var clientRetry = flag.Float64("client-retry", 0.0, "seconds for the client to wait before reconnecting (0 to use browser defaults)")

	flag.Parse()

	log.Print("Redis Address  : ", *redisAddr)
	log.Print("Redis Password : ", *redisPass)
	log.Print("Redis Database : ", *redisDb)
	log.Print("URL Prefix     : ", *urlPrefix)
	log.Print("Listen Address : ", *listenAddr)
	log.Print("Allow POSTs    : ", *allowPosts)
	log.Print("Keep-Alive     : ", *keepAlive)

	if *clientRetry > 0.0 {
		log.Print("Client Retry   : ", *clientRetry)
		clientRetryTime = strconv.Itoa(int(*clientRetry * 1000.0))
	}

	keepAliveTime = time.Duration(*keepAlive)

	client = redis.NewClient(&redis.Options{
		Addr:     *redisAddr,
		Password: *redisPass,
		DB:       *redisDb,
	})
	defer client.Close()

	router := mux.NewRouter()
	subroute := router.PathPrefix(fmt.Sprintf("/%s/", *urlPrefix)).Subrouter()
	subroute.HandleFunc("/{queue}", subscriber).Methods("GET")
	if *allowPosts {
		subroute.HandleFunc("/{queue}", publisher).Methods("POST")
	}

	err := http.ListenAndServe(*listenAddr, router)
	if err != nil {
		log.Fatal("http.ListenAndServe:", err)
	}
}
