package horoscope

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/PuerkitoBio/goquery"
)

var Signes = map[string]string{
	"belier":     "1",
	"taureau":    "2",
	"gemeaux":    "3",
	"cancer":     "4",
	"lion":       "5",
	"vierge":     "6",
	"balance":    "7",
	"scorpion":   "8",
	"sagittaire": "9",
	"capricorne": "10",
	"verseau":    "11",
	"poissons":   "12",
}

var url = "https://www.horoscope.com/us/horoscopes/general/horoscope-general-daily-today.aspx?sign="

func GetHoroscope(signe string) (string, error) {
	resp, err := http.Get(url + Signes[signe])
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status code error: %d %s", resp.StatusCode, resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	var horoscope string
	doc.Find(".main-horoscope > p:nth-child(2)").Each(func(i int, s *goquery.Selection) {
		bold := s.Find("strong").Text()
		horoscope = strings.Trim(s.Text(), " \n")
		horoscope = strings.Replace(horoscope, bold+" - ", "", -1)
	})

	return horoscope, nil
}

type Horoscopes struct {
	sync.Map
}

type HoroscopeErrors struct {
	sync.Map
}

func GetHoroscopes() (*Horoscopes, *HoroscopeErrors) {
	var errors HoroscopeErrors
	var horoscopes Horoscopes
	var wg sync.WaitGroup

	for key, value := range Signes {
		wg.Add(1)
		go func(key, value string) {
			defer wg.Done()
			horoscope, err := GetHoroscope(key)
			if err != nil {
				errors.Store(key, err)
			} else {
				horoscopes.Store(key, horoscope)
			}
		}(key, value)
	}
	wg.Wait()

	return &horoscopes, &errors
}
