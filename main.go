package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/labstack/echo/v4"
)

// -------------------------------------------------------- //
//
//	STRUCTURES
//
// -------------------------------------------------------- //
type Campaign struct {
	ID         int    `json:"ID"`
	Name       string `json:"Name"`
	Key        string `json:"Key"`
	Params     string `json:"Params"`
	CyclesDone int    `json:"CyclesDone"`
	Pages      []Page `json:"Pages"`
}

type Page struct {
	ID            int    `json:"ID"`
	Name          string `json:"Name"`
	URL           string `json:"URL"`
	CycleHitsDone int    `json:"CycleHitsDone"`
	CycleHitsTodo int    `json:"CycleHitsTodo"`
}

type Analytic struct {
	CampaignPageID int
	Date           string
	Type           string
	Params         []AnalyticParam
}

type AnalyticParam struct {
	Name  string
	Value string
}

type Client struct {
	// contains filtered or unexported fields
}

// -------------------------------------------------------- //
//
//	FUNCTIONS
//
// -------------------------------------------------------- //
func GetPageToDispatch(campaign Campaign) Page {
	page := Page{}

	for _, p := range campaign.Pages {
		if p.CycleHitsDone < p.CycleHitsTodo {
			page = p
		}
	}

	return page
}

func ResetCampaignCycles(campaign Campaign) Campaign {
	sum := 0
	for _, p := range campaign.Pages {
		sum += p.CycleHitsDone
	}

	if sum >= 100 {
		for i, p := range campaign.Pages {
			p.CycleHitsDone = 0
			campaign.Pages[i] = p
		}
	}

	campaign.CyclesDone++

	return campaign
}

func UpdatePageCampaignCycles(campaign Campaign, page Page) Campaign {
	for i, p := range campaign.Pages {
		if p.ID == page.ID {
			page.CycleHitsDone++
			campaign.Pages[i] = page
		}
	}

	return campaign
}

func (analytic *Analytic) addParam(param AnalyticParam) []AnalyticParam {
	analytic.Params = append(analytic.Params, param)
	return analytic.Params
}

func sendJSFile(w http.ResponseWriter, r *http.Request) {
	data, err := ioutil.ReadFile("assets/tracking.js")
	if err != nil {
		http.Error(w, "Couldn't read file", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Write(data)
}

func getPageUrl(c echo.Context, page Page) string {

	params, _ := url.ParseQuery(strings.Split(strings.Split(c.Request().URL.String(), "?")[1], "#")[0])
	_url := strings.Split(page.URL, "#")
	pageUrl := _url[0]
	pageUrlParams, _ := url.ParseQuery(page.URL)
	hash := ""

	if len(_url) > 1 {
		hash = "#" + _url[1]
	}

	if len(params) > 0 {
		if len(pageUrlParams) > 1 {
			return fmt.Sprintf("%s&intoid=%d&%s%s",
				pageUrl,
				page.ID,
				params.Encode(),
				hash,
			)
		}
		return fmt.Sprintf("%s?intoid=%d&%s%s",
			pageUrl,
			page.ID,
			params.Encode(),
			hash,
		)
	}

	if len(pageUrlParams) > 1 {
		return fmt.Sprintf("%s&intoid=%d%s",
			pageUrl,
			page.ID,
			hash,
		)
	}

	return fmt.Sprintf("%s?intoid=%d%s",
		pageUrl,
		page.ID,
		hash,
	)
}

func getRedisClient() *redis.Client {
	if os.Getenv("REDIS_TLS") == "false" {
		return redis.NewClient(&redis.Options{
			Addr:     os.Getenv("REDIS_HOST") + ":" + os.Getenv("REDIS_PORT"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       0,
		})
	} else {
		return redis.NewClient(&redis.Options{
			Addr:     os.Getenv("REDIS_HOST") + ":" + os.Getenv("REDIS_PORT"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       0,
			TLSConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		})
	}
}

func saveAnalytic(c echo.Context, rdb *redis.Client, page Page, t string) {
	ctx := context.Background()

	params := c.Request().URL.Query() // Get URL params

	analytic := Analytic{
		page.ID,
		time.Now().Format("2006-01-02"),
		t,
		[]AnalyticParam{},
	}

	if len(params) > 0 {
		for k := range params { // Loop and push each param to analytic struct
			analytic.addParam(AnalyticParam{
				k,
				params.Get(k),
			})
		}
	}

	res, err := json.Marshal(analytic)

	err = rdb.LPush(ctx, "tasks", res).Err()

	if err != nil {
		panic(err)
	}
}

func getPage(campaign Campaign, rdb *redis.Client) Page {
	ctx := context.Background()
	page := GetPageToDispatch(campaign)

	if page == (Page{}) {
		campaign = ResetCampaignCycles(campaign)
		page = GetPageToDispatch(campaign)
	}

	campaign = UpdatePageCampaignCycles(campaign, page)

	res, err := json.Marshal(campaign)
	err = rdb.Set(ctx, "campaign:"+campaign.Key, res, 0).Err()

	if err != nil {
		panic(err)
	}

	return page
}

// -------------------------------------------------------- //
//
//	MAIN
//
// -------------------------------------------------------- //
func main() {
	e := echo.New()

	// ------------------------------------------ //
	//	HOOK (VIEW)
	// ------------------------------------------ //
	e.POST("/hooks/campaign/view", func(c echo.Context) error {
		key := c.FormValue("intoid")

		if key != "" {
			intoid, err := strconv.Atoi(key)

			ctx := context.Background()

			rdb := getRedisClient()

			analytic := Analytic{
				intoid,
				time.Now().Format("2006-01-02"),
				"view",
				[]AnalyticParam{},
			}

			params, _ := c.FormParams()
			fmt.Println("params :", params)

			for k := range params { // Loop and push each param to analytic struct
				analytic.addParam(AnalyticParam{
					k,
					params.Get(k),
				})
			}

			res, err := json.Marshal(analytic)

			err = rdb.LPush(ctx, "tasks", res).Err()

			if err != nil {
				panic(err)
			}
		}

		return c.String(http.StatusOK, "POST !")
	})

	// ------------------------------------------ //
	//	CAMPAIGN
	// ------------------------------------------ //
	e.GET("/:key", func(c echo.Context) error {
		key := c.Param("key")

		if key == "tracking.js" {
			data, err := ioutil.ReadFile("assets/tracking.js")

			if err != nil {
				return c.String(http.StatusNotFound, "Not found")
			}

			output := bytes.Replace(data, []byte("{{APP_URL}}"), []byte(os.Getenv("HTTP_DOMAIN")), -1)

			return c.Blob(http.StatusOK, "application/javascript; charset=utf-8", output)
		}

		ctx := context.Background()

		// ------------------------------------------ //
		//	GET & PAGE FROM REDIS
		// ------------------------------------------ //
		rdb := getRedisClient()
		val, err := rdb.Get(ctx, "campaign:"+key).Result()

		fmt.Println("Key :", key)
		fmt.Println("Campaign :", val)

		if err != nil {
			//panic(err)
			return c.String(http.StatusOK, "Campaign not found")
		}

		campaign := Campaign{}
		json.Unmarshal([]byte(val), &campaign)
		// ------------------------------------------ //

		// ------------------------------------------ //
		//	COMPUTE PAGE & REFRESH REDIS
		// ------------------------------------------ //
		page := getPage(campaign, rdb)
		// ------------------------------------------ //

		// ------------------------------------------ //
		//	PUSH ANALYTIC TO REDIS QUEUE
		// ------------------------------------------ //
		saveAnalytic(c, rdb, page, "hit")
		// ------------------------------------------ //
		return c.Redirect(302, getPageUrl(c, page))
	})

	// ------------------------------------------ //
	//	ROOT
	// ------------------------------------------ //
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "Greetings from the redirector !")
	})

	// ------------------------------------------ //
	//	PING
	// ------------------------------------------ //
	e.GET("/ping", func(c echo.Context) error {
		return c.JSON(http.StatusOK, struct{ Status string }{Status: "OK"})
	})

	e.Logger.Fatal(e.Start(os.Getenv("HTTP_HOST") + ":" + os.Getenv("HTTP_PORT")))
}
