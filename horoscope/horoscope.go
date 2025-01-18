package horoscope

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var signes = map[string]string{
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

var url = "https://www.horoscope.com/fr/horoscopes/general/horoscope-general-du-jour-aujourdhui.aspx?signe="

func GetHoroscope(signe string) (string, error) {
	resp, err := http.Get(url + signes[signe])
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
	doc.Find(".horoscope-content > p:first-child").Each(func(i int, s *goquery.Selection) {
		bold := s.Find("b").Text()
		horoscope = strings.Trim(s.Text(), " \n")
		horoscope = strings.Replace(horoscope, bold+" - ", "", -1)
	})

	return horoscope, nil
}

type Horoscope map[string]string

type HoroscopeError map[string]error

func GetHoroscopes() (Horoscope, HoroscopeError) {
	errors := make(HoroscopeError)
	horoscopes := make(Horoscope)

	ch := make(chan string, len(signes))

	for key, value := range signes {
		go func(key, value string) {
			horoscope, err := GetHoroscope(key)
			if err != nil {
				errors[key] = err
			} else {
				horoscopes[key] = horoscope
			}
			ch <- key
		}(key, value)
	}

	for range signes {
		<-ch
	}

	return horoscopes, errors
}
