package main

import (
	"fmt"
	"net/http"

	spinhttp "github.com/spinframework/spin-go-sdk/v2/http"
)

func init() {
	spinhttp.Handle(func(w http.ResponseWriter, r *http.Request) {
		x := 43 // Experimentally generates a reasonable amount of CPU load
		fmt.Printf("Calculating fib(%d)\n", x)
		fmt.Fprintf(w, "fib(%d) = %d\n", x, fib(x))
	})
}

func fib(n int) int {
	if n < 2 {
		return n
	}
	return fib(n-2) + fib(n-1)
}

func main() {}
