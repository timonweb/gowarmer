package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"
	"strings"
	"github.com/PuerkitoBio/goquery"
)

type PageData struct {
	Response     http.Response
	ResponseTime time.Duration
}

var visited = make(map[string]PageData)
var statusCount = make(map[int]int)
var lock sync.Mutex
var verbose bool
var maxConcurrency int
var username, password string
var customHeaders string


func main() {
	var startURL, sitemapURL string

	flag.StringVar(&startURL, "url", "", "URL to start crawling from")
	flag.StringVar(&sitemapURL, "sitemap", "", "URL of the sitemap.xml")
	flag.BoolVar(&verbose, "v", false, "Show progress of the links being crawled")
	flag.IntVar(&maxConcurrency, "c", 10, "Max number of concurrent crawls")
	flag.StringVar(&username, "username", "", "HTTP basic auth username")
	flag.StringVar(&password, "password", "", "HTTP basic auth password")
	flag.StringVar(&customHeaders, "headers", "", "Custom headers to include in requests (format: Header1:Value1,Header2:Value2,...)")
	flag.Parse()

	if startURL == "" && sitemapURL == "" {
		log.Fatal("Please provide a starting URL using the -url or -sitemap parameter.")
	}

	sem := make(chan bool, maxConcurrency)
	wg := &sync.WaitGroup{}

	if sitemapURL != "" {
		processSitemapURL(sitemapURL, sem, wg)
	} else {
		crawl(startURL, sem, wg)
	}

	wg.Wait()
	report()
}

func sendRequest(u string) (*http.Response, error) {
	client := http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}

	// Add custom headers to the request
	headerPairs := strings.Split(customHeaders, ",")
	for _, h := range headerPairs {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			req.Header.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
	}

	if username != "" && password != "" {
		req.SetBasicAuth(username, password)
	}

	return client.Do(req)
}

func crawl(u string, sem chan bool, wg *sync.WaitGroup) {
	sem <- true
	wg.Add(1)

	go func() {
		defer func() {
			<-sem
			wg.Done()
		}()

		baseURL, _ := url.Parse(u)

		start := time.Now()
		res, err := sendRequest(u)
		responseTime := time.Since(start)
		if err != nil {
			log.Printf("Error fetching %s: %v", u, err)
			return
		}
		defer res.Body.Close()

		lock.Lock()
		visited[u] = PageData{Response: *res, ResponseTime: responseTime}
		statusCount[res.StatusCode]++
		lock.Unlock()

		if verbose {
			fmt.Println("Crawling:", u)
		}

		doc, err := goquery.NewDocumentFromReader(res.Body)
		if err != nil {
			log.Printf("Error reading document %s: %v", u, err)
			return
		}

		doc.Find("a[href]").Each(func(index int, item *goquery.Selection) {
			linkTag := item
			link, exists := linkTag.Attr("href")
			if !exists {
				return
			}

			linkURL, err := url.Parse(link)
			if err != nil {
				return
			}

			if baseURL == nil {
				log.Printf("Error: Base URL could not be parsed for %s", u)
				return
			}

			absoluteURL := baseURL.ResolveReference(linkURL)

			if absoluteURL.Host != baseURL.Host {
				return
			}

			linkStr := absoluteURL.String()

			lock.Lock()
			if _, exists := visited[linkStr]; !exists {
				visited[linkStr] = PageData{Response: http.Response{}, ResponseTime: 0}
				go crawl(linkStr, sem, wg)
			}
			lock.Unlock()
		})
	}()
}

func processSitemapURL(sitemapURL string, sem chan bool, wg *sync.WaitGroup) {
	res, err := sendRequest(sitemapURL)
	if err != nil {
		log.Fatalf("Error fetching sitemap %s: %v", sitemapURL, err)
		return
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Fatalf("Error reading sitemap document %s: %v", sitemapURL, err)
		return
	}

	isIndexSitemap := false

	// Check if it's an index sitemap
	doc.Find("sitemap loc").Each(func(index int, item *goquery.Selection) {
		isIndexSitemap = true
		linkedSitemapURL := item.Text()
		processSitemapURL(linkedSitemapURL, sem, wg) // Recursive call for index sitemaps
	})

	if !isIndexSitemap {
		doc.Find("url loc").Each(func(index int, item *goquery.Selection) {
			link := item.Text()
			crawl(link, sem, wg)
		})
	}
}

func report() {
	fmt.Println("\nCrawling completed")

	// Display each link and its status, with non-200 statuses in red
	fmt.Println("\nDetailed Report:")
	for link, pageData := range visited {
		if pageData.Response.StatusCode != 200 {
			// ANSI escape code for red color: \033[31m
			// ANSI escape code to reset color: \033[0m
			fmt.Printf("\033[31m%s : %v | Response Time: %v\033[0m\n", link, pageData.Response.Status, pageData.ResponseTime)
		} else {
			fmt.Printf("%s : %v | Response Time: %v\n", link, pageData.Response.Status, pageData.ResponseTime)
		}
	}

	// Breakdown by status
	fmt.Println("\nStatus Breakdown:")
	for status, count := range statusCount {
		fmt.Printf("Status %d: %d pages\n", status, count)
	}

	// Total pages crawled
	fmt.Println("\nSummary:")
	totalPages := len(visited)
	fmt.Printf("Total pages crawled: %d\n", totalPages)
}
