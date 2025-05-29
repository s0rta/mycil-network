package crawler

import (
	"encoding/json"
	"fmt"
	"lieu/types"
	"lieu/util"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/queue"
)

// WebringLink represents a link from the webring with its precrawl depth
type WebringLink struct {
	URL   string
	Depth int
}

// the following domains are excluded from crawling & indexing, typically because they have a lot of microblog pages
// (very spammy)
func getBannedDomains(path string) []string {
	return util.ReadList(path, "\n")
}

func getBannedSuffixes(path string) []string {
	return util.ReadList(path, "\n")
}

func getBoringWords(path string) []string {
	return util.ReadList(path, "\n")
}

func getBoringDomains(path string) []string {
	return util.ReadList(path, "\n")
}

func getAboutHeuristics(path string) []string {
	return util.ReadList(path, "\n")
}

func getPreviewQueries(path string) []string {
	previewQueries := util.ReadList(path, "\n")
	if len(previewQueries) > 0 {
		return previewQueries
	} else {
		return []string{"main p", "article p", "section p", "p"}
	}
}

func find(list []string, query string) bool {
	for _, item := range list {
		if item == query {
			return true
		}
	}
	return false
}

func getLink(target string) string {
	// remove anchor links
	if strings.Contains(target, "#") {
		target = strings.Split(target, "#")[0]
	}
	if strings.Contains(target, "?") {
		target = strings.Split(target, "?")[0]
	}
	target = strings.TrimSpace(target)
	// remove trailing /
	return strings.TrimSuffix(target, "/")
}

func getWebringLinks(path string) []WebringLink {
	var links []WebringLink
	candidates := util.ReadList(path, "\n")
	for _, l := range candidates {
		// Parse the format "URL | depth"
		parts := strings.Split(l, " | ")
		if len(parts) != 2 {
			continue
		}
		
		urlStr := strings.TrimSpace(parts[0])
		depthStr := strings.TrimSpace(parts[1])
		
		u, err := url.Parse(urlStr)
		if err != nil {
			continue
		}
		if u.Scheme == "" {
			u.Scheme = "https"
		}
		
		depth := 1 // default depth
		if d, err := strconv.Atoi(depthStr); err == nil {
			depth = d
		}
		
		links = append(links, WebringLink{
			URL:   u.String(),
			Depth: depth,
		})
	}
	return links
}

func getDomains(links []WebringLink) ([]string, []string) {
	var domains []string
	// sites which should have stricter crawling enforced (e.g. applicable for shared sites like tilde sites)
	// pathsites are sites that are passed in which contain path,
	// e.g. https://example.com/site/lupin -> only children pages of /site/lupin/ will be crawled
	var pathsites []string
	for _, link := range links {
		u, err := url.Parse(link.URL)
		if err != nil {
			continue
		}
		domains = append(domains, u.Hostname())
		if len(u.Path) > 0 && (u.Path != "/" || u.Path != "index.html") {
			pathsites = append(pathsites, link.URL)
		}
	}
	return domains, pathsites
}

func findSuffix(suffixes []string, query string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(strings.ToLower(query), suffix) {
			return true
		}
	}
	return false
}

func cleanText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	whitespace := regexp.MustCompile(`\p{Z}+`)
	s = whitespace.ReplaceAllString(s, " ")
	return s
}

func handleIndexing(c *colly.Collector, previewQueries []string, heuristics []string, precrawlDepths map[string]int) {
	c.OnHTML("meta[name=\"keywords\"]", func(e *colly.HTMLElement) {
		domain := e.Request.URL.Hostname()
		depth := precrawlDepths[domain]
		fmt.Println("keywords", cleanText(e.Attr("content")), e.Request.URL, depth)
	})

	c.OnHTML("meta[name=\"description\"]", func(e *colly.HTMLElement) {
		desc := cleanText(e.Attr("content"))
		if len(desc) > 0 && len(desc) < 1500 {
			domain := e.Request.URL.Hostname()
			depth := precrawlDepths[domain]
			fmt.Println("desc", desc, e.Request.URL, depth)
		}
	})

	c.OnHTML("meta[property=\"og:description\"]", func(e *colly.HTMLElement) {
		ogDesc := cleanText(e.Attr("content"))
		if len(ogDesc) > 0 && len(ogDesc) < 1500 {
			domain := e.Request.URL.Hostname()
			depth := precrawlDepths[domain]
			fmt.Println("og-desc", ogDesc, e.Request.URL, depth)
		}
	})

	c.OnHTML("html[lang]", func(e *colly.HTMLElement) {
		lang := cleanText(e.Attr("lang"))
		if len(lang) > 0 && len(lang) < 100 {
			domain := e.Request.URL.Hostname()
			depth := precrawlDepths[domain]
			fmt.Println("lang", lang, e.Request.URL, depth)
		}
	})

	// get page title
	c.OnHTML("title", func(e *colly.HTMLElement) {
		domain := e.Request.URL.Hostname()
		depth := precrawlDepths[domain]
		fmt.Println("title", cleanText(e.Text), e.Request.URL, depth)
	})

	c.OnHTML("body", func(e *colly.HTMLElement) {
		domain := e.Request.URL.Hostname()
		depth := precrawlDepths[domain]
	QueryLoop:
		for i := 0; i < len(previewQueries); i++ {
			// After the fourth paragraph we're probably too far in to get something interesting for a preview
			elements := e.DOM.Find(previewQueries[i])
			for j := 0; j < 4 && j < elements.Length(); j++ {
				element_text := elements.Slice(j, j+1).Text()
				paragraph := cleanText(element_text)
				if len(paragraph) < 1500 && len(paragraph) > 20 {
					if !util.Contains(heuristics, strings.ToLower(paragraph)) {
						fmt.Println("para", paragraph, e.Request.URL, depth)
						break QueryLoop
					}
				}
			}
		}
		paragraph := cleanText(e.DOM.Find("p").First().Text())
		if len(paragraph) < 1500 && len(paragraph) > 0 {
			fmt.Println("para-just-p", paragraph, e.Request.URL, depth)
		}

		// get all relevant page headings
		collectHeadingText("h1", e, depth)
		collectHeadingText("h2", e, depth)
		collectHeadingText("h3", e, depth)
	})
}

func collectHeadingText(heading string, e *colly.HTMLElement, depth int) {
	for _, headingText := range e.ChildTexts(heading) {
		if len(headingText) < 500 {
			fmt.Println(heading, cleanText(headingText), e.Request.URL, depth)
		}
	}
}

func SetupDefaultProxy(config types.Config) error {
	// no proxy configured, go back
	if config.General.Proxy == "" {
		return nil
	}
	proxyURL, err := url.Parse(config.General.Proxy)
	if err != nil {
		return err
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}

	http.DefaultClient = httpClient
	return nil
}

type Mushroom struct {
	Spores   []string `json:"spores"`
	Hyphae   []string `json:"hyphae"`
	ID       string   `json:"id"`
	Location string   `json:"location"`
}

func Precrawl(config types.Config) {
	// setup proxy
	err := SetupDefaultProxy(config)
	if err != nil {
		log.Fatal(err)
	}

	res, err := http.Get(config.General.URL)

	if err != nil {
		log.Fatal(err)
	}

	defer res.Body.Close()

	if res.StatusCode != 200 {
		log.Fatal("status not 200")
	}

	var alreadyCrawled []string
	var seenDomains []string
	var exploredHyphae []string
	var currentDepth = 1

	// Helper function to normalize domain for duplicate detection
	normalizeDomain := func(link string) string {
		u, err := url.Parse(link)
		if err != nil {
			return ""
		}
		domain := u.Hostname()
		// Remove www prefix for comparison
		if strings.HasPrefix(domain, "www.") {
			domain = strings.TrimPrefix(domain, "www.")
		}
		return domain
	}

	var mushroom Mushroom
	if err := json.NewDecoder(res.Body).Decode(&mushroom); err != nil {
		log.Printf("Error decoding JSON from %s: %v", config.General.URL, err)
		return
	}

	// Process initial spores at depth 1
	BANNED := getBannedDomains(config.Crawler.BannedDomains)
	for _, item := range mushroom.Spores {
		link := getLink(item)
		u, err := url.Parse(link)
		// invalid link
		if err != nil {
			continue
		}
		domain := u.Hostname()
		normalizedDomain := normalizeDomain(link)
		
		if find(BANNED, domain) || find(alreadyCrawled, link) || find(seenDomains, normalizedDomain) {
			continue
		}
		fmt.Printf("%s | %d\n", link, currentDepth)
		alreadyCrawled = append(alreadyCrawled, link)
		seenDomains = append(seenDomains, normalizedDomain)
	}

	// Collect initial hyphae to explore
	var currentLevelHyphae []string
	for _, item := range mushroom.Hyphae {
		link := getLink(item)
		if !find(exploredHyphae, link) {
			currentLevelHyphae = append(currentLevelHyphae, link)
		}
	}

	for len(currentLevelHyphae) > 0 {
		currentDepth++
		var nextLevelHyphae []string

		// Process all hyphae at the current level
		for _, hyphaeLink := range currentLevelHyphae {
			if find(exploredHyphae, hyphaeLink) {
				continue
			}

			res, err := http.Get(hyphaeLink)
			exploredHyphae = append(exploredHyphae, hyphaeLink)

			if err != nil {
				log.Printf("Error fetching %s: %v", hyphaeLink, err)
				continue
			}

			defer res.Body.Close()

			var currentMushroom Mushroom
			if err := json.NewDecoder(res.Body).Decode(&currentMushroom); err != nil {
				log.Printf("Error decoding JSON from %s: %v", hyphaeLink, err)
				continue
			}

			// Process spores at current depth
			for _, item := range currentMushroom.Spores {
				link := getLink(item)
				u, err := url.Parse(link)
				// invalid link
				if err != nil {
					continue
				}
				domain := u.Hostname()
				normalizedDomain := normalizeDomain(link)
				
				if find(BANNED, domain) || find(alreadyCrawled, link) || find(seenDomains, normalizedDomain) {
					continue
				}
				fmt.Printf("%s | %d\n", link, currentDepth)
				alreadyCrawled = append(alreadyCrawled, link)
				seenDomains = append(seenDomains, normalizedDomain)
			}

			// Collect hyphae for next level
			for _, item := range currentMushroom.Hyphae {
				link := getLink(item)
				if !find(exploredHyphae, link) && !find(nextLevelHyphae, link) {
					nextLevelHyphae = append(nextLevelHyphae, link)
				}
			}
		}

		// Move to next level
		currentLevelHyphae = nextLevelHyphae
	}
}

func Crawl(config types.Config) {
	// setup proxy
	err := SetupDefaultProxy(config)
	if err != nil {
		log.Fatal(err)
	}
	SUFFIXES := getBannedSuffixes(config.Crawler.BannedSuffixes)
	links := getWebringLinks(config.Crawler.Webring)
	domains, pathsites := getDomains(links)
	initialDomain := config.General.URL

	// Create a map to store precrawl depths for each domain
	precrawlDepths := make(map[string]int)
	for _, link := range links {
		u, err := url.Parse(link.URL)
		if err != nil {
			continue
		}
		precrawlDepths[u.Hostname()] = link.Depth
	}

	// TODO: introduce c2 for scraping links (with depth 1) linked to from webring domains
	// instantiate default collector
	c := colly.NewCollector(
		colly.MaxDepth(3),
	)
	if config.General.Proxy != "" {
		c.SetProxy(config.General.Proxy)
	}

	q, _ := queue.New(
		5, /* threads */
		&queue.InMemoryQueueStorage{MaxSize: 100000},
	)

	for _, link := range links {
		q.AddURL(link.URL)
	}

	c.UserAgent = "MoldWeb_crawler"
	c.AllowedDomains = domains
	c.AllowURLRevisit = false
	c.DisallowedDomains = getBannedDomains(config.Crawler.BannedDomains)

	delay, _ := time.ParseDuration("200ms")
	c.Limit(&colly.LimitRule{DomainGlob: "*", Delay: delay, Parallelism: 3})

	boringDomains := getBoringDomains(config.Crawler.BoringDomains)
	boringWords := getBoringWords(config.Crawler.BoringWords)
	previewQueries := getPreviewQueries(config.Crawler.PreviewQueries)
	heuristics := getAboutHeuristics(config.Data.Heuristics)

	// on every a element which has an href attribute, call callback
	c.OnHTML("a[href]", func(e *colly.HTMLElement) {

		if e.Response.StatusCode >= 400 || e.Response.StatusCode <= 100 {
			return
		}

		link := getLink(e.Attr("href"))
		if findSuffix(SUFFIXES, link) {
			return
		}

		link = e.Request.AbsoluteURL(link)
		u, err := url.Parse(link)
		if err != nil {
			return
		}

		outgoingDomain := u.Hostname()
		currentDomain := e.Request.URL.Hostname()

		// log which site links to what
		if !util.Contains(boringWords, link) && !util.Contains(boringDomains, link) {
			currentDepth := precrawlDepths[currentDomain]
			// log precrawl depths
			// fmt.Println("currentDepth", currentDomain, outgoingDomain, currentDepth)
			if !find(domains, outgoingDomain) {
				fmt.Println("non-webring-link", link, e.Request.URL, currentDepth)
				// solidarity! someone in the webring linked to someone else in it
			} else if outgoingDomain != currentDomain && outgoingDomain != initialDomain && currentDomain != initialDomain {
				fmt.Println("webring-link", link, e.Request.URL, currentDepth)
			}
		}

		// rule-based crawling
		var pathsite string
		for _, s := range pathsites {
			if strings.Contains(s, outgoingDomain) {
				pathsite = s
				break
			}
		}
		// the visited site was a so called »pathsite», a site with restrictions on which pages can be crawled (most often due to
		// existing on a shared domain)
		if pathsite != "" {
			// make sure we're only crawling descendents of the original path
			if strings.HasPrefix(link, pathsite) {
				q.AddURL(link)
			}
		} else {
			// visits links from AllowedDomains
			q.AddURL(link)
		}
	})

	handleIndexing(c, previewQueries, heuristics, precrawlDepths)

	// start scraping
	q.Run(c)
}
