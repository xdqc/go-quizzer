package solver

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var (
	google_URL = "http://www.google.com/search?"
	baidu_URL  = "http://www.baidu.com/s?"
	so360_URL  = "http://www.so.com/s?"
)

//GetFromAPI searh the quiz via popular search engins
func GetFromAPI(quiz string, options []string) map[string]int {
	res := make(map[string]int, len(options))
	for _, option := range options {
		res[option] = 0
	}

	search := make(chan string, 4)
	done := make(chan bool, 1)
	tx := time.Now()

	go searchFeelingLucky(quiz, options, search)
	go searchGoogle(quiz, options, search)
	go searchBaidu(quiz, options, search)
	go searchGoogleWithOptions(quiz, options, search)

	println("\n.......................searching..............................\n")
	rawStr := "                    "
	count := 4
	go func() {
		for {
			s, more := <-search
			if more {
				log.Println("search received...")
				rawStr += s
				count--
				if count == 0 {
					done <- true
					return
				}
			}
		}
	}()
	select {
	case <-done:
		fmt.Println("search done")
	case <-time.After(2 * time.Second):
		fmt.Println("search timeout")
	}
	rawStr += "                    "
	tx2 := time.Now()
	log.Printf("Searching time: %d ms\n", tx2.Sub(tx).Nanoseconds()/1e6)

	// filter out non alphanumeric/chinese/space
	re := regexp.MustCompile("[^\\w\\p{Han} ]+")
	str := re.ReplaceAllString(rawStr, "")
	// println("str:\n" + str)

	qz := re.ReplaceAllString(quiz, "")
	// sliding window, count the common chars between [neighbor of the option in search text] and [quiz]
	width := len(qz)
	if width > 20 {
		width = 20 //max window size
	}
	for _, option := range options {
		// res[option] = strings.Count(str, re.ReplaceAllString(option, ""))
		opt := re.ReplaceAllString(option, "")
		for i := range str {
			// find the index of option in the search text
			if strings.Index(str[i:], opt) == 0 {
				window := str[i-width : i+len(opt)+width]
				if strings.Contains(qz, "上一") || strings.Contains(qz, "之前") {
					window = str[i+len(opt) : i+len(opt)+width]
				} else if strings.Contains(qz, "下一") || strings.Contains(qz, "之后") {
					window = str[i-width : i]
				}

				for _, ch := range window {
					if strings.ContainsRune(qz, ch) {
						res[option]++
					}
				}
				print(option, res[option], "\t")
			}
		}
		println()
	}

	// if all option got 0 match, search the each option.trimLastChar (xx省 -> xx)
	// if totalCount == 0 {
	// 	for _, option := range options {
	// 		res[option] = strings.Count(str, option[:len(option)-1])
	// 	}
	// }

	// For no-number option, add count to its superstring option count （米波 add to 毫米波)
	re = regexp.MustCompile("[\\d]+")
	for _, opt := range options {
		if !re.MatchString(opt) {
			for _, subopt := range options {
				if opt != subopt && strings.Contains(opt, subopt) {
					res[opt] += res[subopt]
				}
			}
		}
	}

	// For negative quiz, flip the count to negative number (dont flip quoted negative word)
	re = regexp.MustCompile("「[^」]*[不][^」]*」")
	if (strings.Contains(quiz, "不") || strings.Contains(quiz, "没有") || strings.Contains(quiz, "未在")) &&
		!(strings.Contains(quiz, "不同") || strings.Contains(quiz, "不充分") || strings.Contains(quiz, "不对称") || re.MatchString(quiz)) {
		for _, option := range options {
			res[option] = -res[option] - 1
		}
	}

	tx3 := time.Now()
	log.Printf("Processing time %d ms\n", tx3.Sub(tx2).Nanoseconds()/1e6)

	return res
}

func searchBaidu(quiz string, options []string, c chan string) {
	values := url.Values{}
	values.Add("wd", quiz+" site:baidu.com")
	req, _ := http.NewRequest("GET", baidu_URL+values.Encode(), nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp == nil {
		c <- ""
	} else {
		doc, _ := goquery.NewDocumentFromReader(resp.Body)
		text := doc.Find("#content_left .t").Text() + doc.Find("#content_left .c-abstract").Text() + doc.Find("#content_left .m").Text() //.m ~zhidao
		c <- text                                                                                                                        // 2x weight
	}
}

func searchBaiduWithOptions(quiz string, options []string, c chan string) {
	values := url.Values{}
	values.Add("wd", quiz+" "+strings.Join(options, " "))
	req, _ := http.NewRequest("GET", baidu_URL+values.Encode(), nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp == nil {
		c <- ""
	} else {
		doc, _ := goquery.NewDocumentFromReader(resp.Body)
		c <- doc.Find("#content_left .t").Text() + doc.Find("#content_left .c-abstract").Text()
	}
}

func searchGoogle(quiz string, options []string, c chan string) {
	values := url.Values{}
	values.Add("q", quiz)
	req, _ := http.NewRequest("GET", google_URL+values.Encode(), nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp == nil {
		c <- ""
	} else {
		doc, _ := goquery.NewDocumentFromReader(resp.Body)
		str := doc.Find(".r").Text() + doc.Find(".st").Text() + doc.Find(".P1usbc").Text() //.P1usbc ~wiki
		c <- str
	}
}

func searchGoogleWithOptions(quiz string, options []string, c chan string) {
	values := url.Values{}
	values.Add("q", quiz+" \""+strings.Join(options, "\" OR \"")+"\"")
	req, _ := http.NewRequest("GET", google_URL+values.Encode(), nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp == nil {
		c <- ""
	} else {
		doc, _ := goquery.NewDocumentFromReader(resp.Body)
		text := doc.Find(".r").Text() + doc.Find(".st").Text() + doc.Find(".P1usbc").Text() //.P1usbc ~wiki
		c <- text                                                                           // 2x weight
	}
}

func searchBing(quiz string, options []string, c chan string) {
	values := url.Values{}
	values.Add("q", quiz)
	req, _ := http.NewRequest("GET", so360_URL+values.Encode(), nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp == nil {
		c <- ""
	} else {
		doc, _ := goquery.NewDocumentFromReader(resp.Body)
		str := doc.Find(".res-desc").Text()
		log.Println("so360 results ----- ", str)
		c <- str
	}
}

func searchFeelingLucky(quiz string, options []string, c chan string) {
	values := url.Values{}
	values.Add("q", quiz)
	values.Add("btnI", "") //click I'm feeling lucky! button
	req, _ := http.NewRequest("GET", google_URL+values.Encode(), nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	log.Println("-------- luck url:  " + resp.Request.URL.Host + resp.Request.URL.Path + " /// " + resp.Request.Host)
	if resp == nil || resp.Request.Host == "www.google.com" {
		c <- ""
	} else if resp.Request.URL.Host == "zh.wikipedia.org" {
		doc, _ := goquery.NewDocumentFromReader(resp.Body)
		text := doc.Find(".mw-parser-output").Text()
		if len(text) > 5000 {
			text = text[:5000]
		}
		c <- text
	} else if resp.Request.URL.Host == "baike.baidu.com" {
		doc, _ := goquery.NewDocumentFromReader(resp.Body)
		text := doc.Find(".para").Text() + doc.Find(".basicInfo-item").Text()
		if len(text) > 5000 {
			text = text[:5000]
		}
		c <- text
	} else {
		doc, _ := goquery.NewDocumentFromReader(resp.Body)
		text := doc.Text()
		log.Println(text)
		if len(text) > 5000 {
			text = text[:5000]
		}
		c <- text
	}
}
