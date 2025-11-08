package main

import (
	"fmt"
	"io"
	"net/http"

	spinhttp "github.com/spinframework/spin-go-sdk/v2/http"

	"github.com/spinframework/spin-go-sdk/v2/redis"
	"github.com/spinframework/spin-go-sdk/v2/variables"
)

var rdb *redis.Client

func init() {
	spinhttp.Handle(func(w http.ResponseWriter, r *http.Request) {
		redisEndpoint, err := variables.Get("redis_endpoint")
		if err != nil {
			http.Error(w, "unable to parse variable 'redis_endpoint'", http.StatusInternalServerError)
			return
		}

		if redisEndpoint == "" {
			http.Error(w, "cannot find 'redis_endpoint' environment variable", http.StatusInternalServerError)
			return
		}

		rdb = redis.NewClient(redisEndpoint)

		reqKey := r.Header.Get("x-key")
		if reqKey == "" {
			http.Error(w, "you must include the 'x-key' header in your request", http.StatusBadRequest)
			return
		}

		if r.Method == "GET" {
			value, err := rdb.Get(reqKey)
			if err != nil {
				http.Error(w, fmt.Sprintf("no value found for key '%s'", reqKey), http.StatusNotFound)
				return
			}

			w.WriteHeader(http.StatusOK)
			w.Write(value)
			return

		} else if r.Method == "PUT" {
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, fmt.Sprintf("error reading request body: %w", err), http.StatusInternalServerError)
			}
			defer r.Body.Close()

			if err := rdb.Set(reqKey, bodyBytes); err != nil {
				http.Error(w, fmt.Sprintf("unable to add value for key '%s' to database: %w", reqKey, err), http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusCreated)
			return

		} else if r.Method == "DELETE" {
			_, err := rdb.Del(reqKey)
			if err != nil {
				http.Error(w, fmt.Sprintf("error deleting value for key '%w'", err), http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusOK)
			return

		} else {
			http.Error(w, fmt.Sprintf("method %q is not supported, so please try again using 'GET' or 'PUT' for the HTTP method", r.Method), http.StatusBadRequest)
			return
		}
	})
}

func main() {}
