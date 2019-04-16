package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis"
)

type universalHandler struct {
	client          *redis.Client
	pubsubPrefix    string
	streamPrefix    string
	keepAliveTime   time.Duration
	clientRetryTime string
}

type message struct {
	id     string
	source string
	text   string
}

type sender struct {
	source string
	send   func(req *http.Request) (string, error)
}

type receiver struct {
	source   string
	lastId   string
	messages chan message
	shutdown func() error
}

func NewPubSubReceiver(source string, client *redis.Client) *receiver {
	receiver := &receiver{
		source:   source,
		messages: make(chan message),
	}

	go func() {
		pubsub := client.Subscribe(source)
		receiver.shutdown = pubsub.Close

		channel := pubsub.Channel()
		for {
			evt := <-channel
			if evt == nil {
				break
			}
			if evt.Payload == "" {
				continue
			}

			receiver.messages <- message{
				source: evt.Channel,
				text:   evt.Payload,
			}
		}

		close(receiver.messages)
	}()

	return receiver
}

func NewPubSubSender(source string, client *redis.Client) *sender {
	sender := &sender{
		source: source,
		send: func(req *http.Request) (string, error) {
			message, err := ioutil.ReadAll(req.Body)
			if err != nil {
				return "", err
			}

			subscribers, err := client.Publish(source, string(message)).Result()
			if err != nil {
				return "", err
			} else {
				return strconv.FormatInt(subscribers, 10), nil
			}
		},
	}

	return sender
}

func NewStreamReceiver(source string, lastId string, client *redis.Client) *receiver {
	if lastId == "" {
		lastId = "0"
	}

	receiver := &receiver{
		source:   source,
		lastId:   lastId,
		messages: make(chan message),
	}

	go func() {
	xreader:
		for {
			xrr, err := client.XRead(&redis.XReadArgs{
				Block: time.Duration(100) * time.Millisecond,
				Streams: []string{
					receiver.source,
					receiver.lastId,
				},
			}).Result()
			if err != nil {
				if err == redis.Nil {
					continue xreader
				}

				msg := "Stream Receive Failed: " + err.Error()
				log.Print(msg)
				break xreader
			}

			for _, wad := range xrr {
				for _, evt := range wad.Messages {
					lines := make([]string, len(evt.Values))
					i := 0
					for key, val := range evt.Values {
						lines[i] = fmt.Sprintf("%s=%s", key, val)
						i++
					}

					receiver.messages <- message{
						source: wad.Stream,
						id:     evt.ID,
						text:   strings.Join(lines, "\n"),
					}

					receiver.lastId = evt.ID
				}
			}
		}

		close(receiver.messages)
	}()

	return receiver
}

func NewStreamSender(source string, client *redis.Client) *sender {
	sender := &sender{
		source: source,
		send: func(req *http.Request) (string, error) {
			payload := &redis.XAddArgs{
				Stream: source,
				ID:     "*",
			}

			err := req.ParseForm()
			if err != nil {
				return "", err
			}

			if len(req.PostForm) < 1 {
				contentType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
				if err != nil {
					return "", err
				}

				body, err := ioutil.ReadAll(req.Body)
				if err != nil {
					return "", err
				}

				payload.Values = make(map[string]interface{}, 1)

				switch contentType {
				case "application/json":
					if !json.Valid(body) {
						return "", errors.New("invalid JSON")
					}
					payload.Values["json"] = body
				case "text/plain":
					payload.Values["text"] = body
				default:
					return "", fmt.Errorf("unknown Content-Type: %v", contentType)
				}

			} else {
				payload.Values = make(map[string]interface{}, len(req.PostForm))
				for key, vals := range req.PostForm {
					// When multiple values, take the last one
					payload.Values[key] = vals[len(vals)-1:][0]
				}
			}

			return client.XAdd(payload).Result()
		},
	}

	return sender
}

func (handler *universalHandler) subscriber(res http.ResponseWriter, req *http.Request) {
	prefix := path.Dir(req.URL.Path)
	source := path.Base(req.URL.Path)

	// https://www.w3.org/TR/2011/WD-eventsource-20110310/#last-event-id
	lastId := req.Header.Get("Last-Event-ID")

	var receiver *receiver

	switch prefix {
	case handler.pubsubPrefix:
		receiver = NewPubSubReceiver(source, handler.client)
	case handler.streamPrefix:
		receiver = NewStreamReceiver(source, lastId, handler.client)
	default:
		msg := fmt.Sprint("unhandled path: ", prefix)
		log.Print(msg)
		http.Error(res, msg, http.StatusNotFound)
		return
	}

	flusher, ok := res.(http.Flusher)
	if !ok {
		http.Error(res, "Streaming Unsupported", http.StatusInternalServerError)
		return
	}

	defer func() {
		if receiver.shutdown != nil {
			err := receiver.shutdown()
			if err != nil {
				msg := "messenger shutdown error: " + err.Error()
				log.Print(msg)
			}
		}
	}()

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
		case msg := <-receiver.messages:
			if msg.text == "" {
				// Once a channel closes, default values get returned to look for empty text which cannot ordinarily
				// happen
				return
			}

			if msg.id != "" {
				_, err = res.Write([]byte("id: " + msg.id + "\n"))
				if err != nil {
					msg := "Event ID Transmit Failed: " + err.Error()
					log.Print(msg)
					return
				}
			}

			_, err = res.Write([]byte("event: " + msg.source + "\n"))
			if err != nil {
				msg := "Event Name Transmit Failed: " + err.Error()
				log.Print(msg)
				return
			}

			hunks := strings.Split(msg.text, "\n")
			for index := range hunks {
				_, err = res.Write([]byte("data: " + hunks[index] + "\n"))
				if err != nil {
					msg := "Message Transmit Failed: " + err.Error()
					log.Print(msg)
					return
				}
			}
			_, err = res.Write([]byte("\n"))
			if err != nil {
				msg := "Message Transmit Failed: " + err.Error()
				log.Print(msg)
				return
			}
		case <-timeout:
			_, err := res.Write([]byte(": keep-alive\n\n"))
			if err != nil {
				msg := "Keepalive Transmit Failed: " + err.Error()
				log.Print(msg)
				return
			}
		// https://stackoverflow.com/a/53966322
		case <-req.Context().Done():
			return
		}
	}
}

func (handler *universalHandler) publisher(res http.ResponseWriter, req *http.Request) {
	prefix := path.Dir(req.URL.Path)
	source := path.Base(req.URL.Path)

	var sender *sender

	switch prefix {
	case handler.pubsubPrefix:
		sender = NewPubSubSender(source, handler.client)
	case handler.streamPrefix:
		sender = NewStreamSender(source, handler.client)
	default:
		msg := fmt.Sprint("unhandled path: ", prefix)
		log.Print(msg)
		http.Error(res, msg, http.StatusNotFound)
		return
	}

	result, err := sender.send(req)
	if err != nil {
		msg := fmt.Sprint("Error submitting message: ", err.Error())
		log.Print(msg)
		http.Error(res, msg, http.StatusInternalServerError)
		return
	}

	res.Header().Set("Cache-Control", "no-cache")
	res.Header().Set("Content-Type", "text/plain")
	res.WriteHeader(http.StatusOK)
	_, err = res.Write([]byte(result))
	if err != nil {
		msg := "Publish Response Failed: " + err.Error()
		log.Print(msg)
		return
	}
}

func (handler *universalHandler) ServeHTTP(res http.ResponseWriter, req *http.Request) {
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
	var maxRedisConnections = flag.Int("max-redis-connections", 10*runtime.NumCPU(), "maximum number of redis connections in the pool")
	var listenAddr = flag.String("listen-addr", "localhost:8080", "listen address")
	var pubsubPrefix = flag.String("pubsub-prefix", "", "pubsub URL prefix")
	var streamPrefix = flag.String("stream-prefix", "", "stream URL prefix")
	var keepAlive = flag.Int("keepalive", 30, "seconds between keep-alive messages (0 to disable)")
	var clientRetry = flag.Float64("client-retry", 0.0, "seconds for the client to wait before reconnecting (0 to use browser defaults)")

	flag.Parse()

	if *pubsubPrefix == "" && *streamPrefix == "" {
		log.Fatal("Must set pubsib-prefix or stream-prefix")
	}

	log.Print("Redis Address     : ", *redisAddr)
	log.Print("Redis Password    : ", *redisPass)
	log.Print("Redis Database    : ", *redisDb)
	log.Print("Max Connections   : ", *maxRedisConnections)
	log.Print("Listen Address    : ", *listenAddr)
	if *pubsubPrefix != "" {
		log.Print("PubSub URL Prefix : ", *pubsubPrefix)
	}
	if *streamPrefix != "" {
		log.Print("Stream URL Prefix : ", *streamPrefix)
	}
	log.Print("Keep-Alive        : ", *keepAlive)

	var clientRetryTime string
	if *clientRetry > 0.0 {
		log.Print("Client Retry   : ", *clientRetry)
		clientRetryTime = strconv.Itoa(int(*clientRetry * 1000.0))
	}

	client := redis.NewClient(&redis.Options{
		Addr:     *redisAddr,
		Password: *redisPass,
		DB:       *redisDb,
		PoolSize: *maxRedisConnections,
	})

	server := http.Server{
		Addr: *listenAddr,
		Handler: &universalHandler{
			client:          client,
			pubsubPrefix:    *pubsubPrefix,
			streamPrefix:    *streamPrefix,
			keepAliveTime:   time.Duration(*keepAlive),
			clientRetryTime: clientRetryTime,
		},
	}

	log.Printf("Listening on %s", server.Addr)

	log.Fatal(server.ListenAndServe())
}
