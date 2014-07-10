// Package golangstuffs is a pile o' Go code that I'm keeping
// up on github. There's no rhyme or reason here.
package golangstuffs

// A full working example might look like:
// package main

// import (
//  "fmt"
//  "github.com/hobbeswalsh/golangstuffs"
// )

// func main() {
//  urls := []string{
//      "http://www.amazon.com",
//      "http://www.nytimes.com",
//      "http://www.reddit.com",
//      "http://www.slashdot.org",
//      "http://www.google.com"}
//  c := make(chan golangstuffs.FetchResult)
//  go golangstuffs.MultiFetch(urls, 10000, c)
//  for {
//      select {

//      case result := <-c:
//          fmt.Println(result.StatusCode)
//          fmt.Println(result.Url)
//          fmt.Println(result.RoundTripTime)
//          fmt.Println(string(result.Content))

//      default:
//      }
//  }
// }

import (
	"io/ioutil"
	"net/http"
	"time"
)

type FetchResult struct {
	Content       []byte
	RoundTripTime int64
	StatusCode    int
	Url           string
}

func fetchWebPage(url string, c chan FetchResult) {
	beforeFetch := time.Now().UnixNano()
	resp, err := http.Get(url)
	if err != nil {
		return
	}
	afterFetch := time.Now().UnixNano()
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	c <- FetchResult{
		body,
		afterFetch - beforeFetch,
		resp.StatusCode,
		url,
	}
}

// MultiFetch fetches several urls in parallel and keeps track of the results of those fetches.
// It is intended to be run as a goroutine; you supply it with a list of urls, a sleep timer
// (in milliseconds), and a channel in which it should place its FetchResults. The caller
// of this function is responsible for watching the channel for results.
func MultiFetch(urls []string, sleepTime time.Duration, c chan FetchResult) {
	multiFetchChannel := make(chan FetchResult)
	for _, url := range urls {
		go func(url string) {
			for {
				fetchWebPage(url, multiFetchChannel)
				time.Sleep(sleepTime * time.Millisecond)
			}
		}(url)
	}

	for {
		select {
		case fr := <-multiFetchChannel:
			c <- fr

		default:

		}
	}
}
