package main

import (
	"fmt"
	"net/http"

	spinhttp "github.com/spinframework/spin-go-sdk/v2/http"
	"github.com/spinframework/spin-go-sdk/v2/variables"
)

func init() {
	spinhttp.Handle(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		greetee, err := variables.Get("greetee")
		if err != nil {
			fmt.Fprintf(w, "err: %s\n", err)
			return
		}

		fmt.Fprintf(w, "Hello %s!\n", greetee)
	})
}

func main() {}
