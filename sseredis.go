package main

import (
	"io/ioutil"
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/vmihailenco/redis"
)

var client *redis.Client

func subscriber(res http.ResponseWriter, req *http.Request) {
	flusher, ok := res.(http.Flusher)
	if !ok {
		http.Error(res, "Streaming Unsupported", http.StatusInternalServerError)
		return
	}

	pubsub, err := client.PubSubClient()
	if err != nil {
		http.Error(res, "Redis Unavailable: " + err.Error(), http.StatusInternalServerError)
		return
	}
	defer pubsub.Close()

	vars := mux.Vars(req)
	queue := vars["queue"]

	channel, err := pubsub.Subscribe(queue)
	if err != nil {
		http.Error(res, "Subscribe Failed: " + err.Error(), http.StatusInternalServerError)
		return
	}

	res.Header().Set("Access-Control-Allow-Origin", "*")
	res.Header().Set("Cache-Control", "no-cache")
	res.Header().Set("Connection", "keep-alive")
	res.Header().Set("Content-Type", "text/event-stream")

	for {
		msg := <- channel
		if msg.Err != nil {
			http.Error(res, "Message Receive Failed: " + msg.Err.Error(), http.StatusInternalServerError)
			return
		}

		if msg.Message != "" {
			_, err := res.Write([]byte("data: " + msg.Message + "\n\n"))
			if err != nil {
				http.Error(res, "Message Transmit Failed: " + err.Error(), http.StatusInternalServerError)
				return
			}

			flusher.Flush()
		}
	}
}

func publisher(res http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	queue := vars["queue"]

	msg, err := ioutil.ReadAll(req.Body)
	if err != nil {
		http.Error(res, "Message Transmit Failed: " + err.Error(), http.StatusInternalServerError)
		return
	}

	client.Publish(queue, string(msg))

	res.Header().Set("Access-Control-Allow-Origin", "*")
	res.Header().Set("Cache-Control", "no-cache")
	res.Header().Set("Connection", "keep-alive")
	res.Header().Set("Content-Type", "text/event-stream")

	_, err = res.Write([]byte("OK\n"))
	if err != nil {
		http.Error(res, "Message Transmit Failed: " + err.Error(), http.StatusInternalServerError)
		return
	}
}

func main() {
	var redisAddr  = flag.String("redis-addr",  "localhost:6379", "redis address")
	var redisPass  = flag.String("redis-pass",  "",               "redis password")
	var redisDb    = flag.Int   ("redis-db",    -1,               "redis database number")
	var urlPrefix  = flag.String("url-prefix",  "redis",          "URL prefix")
	var listenAddr = flag.String("listen-addr", "localhost:8080", "listen address")
	var allowPosts = flag.Bool  ("allow-posts", false,            "allow POSTing to the queue")

	flag.Parse()

	log.Print("Redis Address  : ", *redisAddr)
	log.Print("Redis Password : ", *redisPass)
	log.Print("Redis Database : ", *redisDb)
	log.Print("URL Prefix     : ", *urlPrefix)
	log.Print("Listen Address : ", *listenAddr)
	log.Print("Allow POSTs    : ", *allowPosts)

	client = redis.NewTCPClient(*redisAddr, *redisPass, int64(*redisDb))
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
