package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/cardigann/releaseinfo"
)

const baseURL = "http://www.addic7ed.com"
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.36"

var preferedLangs = []string{"English", "Turkish"}

type Addic7ed struct {
	username string
	password string
	c        *http.Client
	isAvail  bool
}

func NewAddic7ed() *Addic7ed {
	username := os.Getenv("SUBS_ADDIC7ED_USERNAME")
	password := os.Getenv("SUBS_ADDIC7ED_PASSWORD")

	c := &http.Client{
		// disable redirect
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 10 * time.Second,
	}

	ad := &Addic7ed{
		username: username,
		password: password,
		c:        c,
	}

	params := &url.Values{}
	params.Set("user", username)
	params.Set("password", password)
	params.Set("Submit", "Log in")

	req, err := http.NewRequest("POST", baseURL+"/dologin.php", strings.NewReader(params.Encode()))
	if err != nil {
		log.Printf("Error creating request: %v\n", err)
		return ad

	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := ad.c.Do(req)
	if err != nil {
		return ad
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Failed to authenticate. StatusCode: %v\n", resp.StatusCode)
		return ad
	}

	ad.isAvail = true
	return ad
}

func (a *Addic7ed) Available() bool {
	return a.isAvail
}

func (a *Addic7ed) String() string {
	return "addic7ed"
}

func (a *Addic7ed) Query(ctx context.Context, q string) ([]subtitle, error) {
	ep, err := releaseinfo.Parse(q)
	if err != nil {
		return nil, fmt.Errorf("error parsing release info for %q: %v", q, err)
	}

	ids, err := a.seriesIds()
	if err != nil {
		return nil, fmt.Errorf("error fetching series ids: %v", err)
	}

	title := ep.SeriesTitleInfo.TitleWithoutYear
	title = strings.ToLower(title)
	titleWithYear := fmt.Sprintf("%v (%v)", title, ep.SeriesTitleInfo.Year)

	id := ids.in(title, titleWithYear)
	if id == "" {
		return nil, fmt.Errorf("Show ID could not be found for %q\n", q)
	}

	req, err := http.NewRequest("GET", baseURL+"/show/"+id+"?season="+strconv.Itoa(ep.SeasonNumber), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req = req.WithContext(ctx)

	resp, err := a.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 304 {
		return nil, fmt.Errorf("Too many requests")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Could not list subs. Status code: %v", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromResponse(resp)
	if err != nil {
		return nil, err
	}

	var subs []subtitle
	doc.Find("tr.epeven").Each(func(_ int, tr *goquery.Selection) {
		var sub subtitle
		tr.Find("td").Each(func(i int, td *goquery.Selection) {
			v := td.Text()
			switch i {
			case 0: // season
				sub.season = v
			case 1: // episode
				sub.episode = v
			case 2: // title + page link
				sub.title = v

				href, ok := td.Find("a").Attr("href")
				if !ok {
					return
				}
				sub.pageURL = baseURL + href
			case 3: // language
				sub.language = v
			case 4: // release
				sub.release = v
			case 5: // status (completed?)
				sub.status = v
			case 6: // hearing impaired
				if v != "" {
					sub.hearingImpaired = true
				}
			case 7: // corrected
				// ignore, not needed.
			case 8: // HD
				// ignore, not needed.
			case 9: // download url
				href, ok := td.Find("a").Attr("href")
				if !ok {
					return
				}
				// url is in the form of /updated/<int>/<int>/<int>
				sub.downloadURL = baseURL + href
			case 10:
			}
		})

		if !isLangPrefered(sub.language) {
			debugf("%q is not a prefered language, skipping...\n", sub.language)
			return
		}

		if sub.status != "Completed" {
			debugf("Skipping subtitles with status %q\n", sub.status)
			return
		}

		subs = append(subs, sub)
	})

	return subs, nil
}

func (a *Addic7ed) Download(ctx context.Context, sub subtitle) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", sub.downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("Error creating download request: %v", err)
	}
	req = req.WithContext(ctx)
	req.Header.Set("Referer", sub.pageURL)

	resp, err := a.c.Do(req)
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}

func (a *Addic7ed) seriesIds() (seriesIds, error) {
	resp, err := a.c.Get(baseURL + "/shows.php")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Could not fetch show ids. Status code: %v", resp.StatusCode)
	}

	q, err := goquery.NewDocumentFromResponse(resp)
	if err != nil {
		return nil, err
	}

	showIds := make(seriesIds)
	q.Find(`td.version > h3 > a[href^="/show/"]`).Each(func(i int, s *goquery.Selection) {
		show := s.Text()
		show = strings.ToLower(show)
		// href includes the show id, which is why we are searching for.
		// it looks like this: <a href="/show/:someid:">Derek</a>
		href, ok := s.Attr("href")
		if !ok {
			log.Printf("show id could not be found for %q\n", show)
			return
		}

		id := strings.TrimPrefix(href, "/show/")
		showIds[show] = id
		return
	})

	return showIds, nil
}

type seriesIds map[string]string

func (s seriesIds) in(titles ...string) string {
	for _, title := range titles {
		for k, v := range s {
			if k == title {
				return v
			}
		}
	}
	return ""
}

type subtitle struct {
	title           string
	season          string
	episode         string
	language        string
	release         string
	status          string
	hearingImpaired bool
	downloadURL     string
	pageURL         string
}

func (s subtitle) String() string {
	var hearingImpaired string
	if s.hearingImpaired {
		hearingImpaired = "✔"
	}
	return fmt.Sprintf("%v\t%v\t%v\t%v\t%v\t%v\n", s.season, s.episode, s.title, s.language, s.release, hearingImpaired)
}

func isLangPrefered(l string) bool {
	for _, lang := range preferedLangs {
		if l == lang {
			return true
		}
	}
	return false
}
