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
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/cesbo/go-logtail"
	"github.com/go-redis/redis/v8"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/rs/zerolog"
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

// -------------------------------------------------------- //
//
//	FUNCTIONS
//
// -------------------------------------------------------- //
func PrettyStruct(data interface{}) (string, error) {
	val, err := json.MarshalIndent(data, "", "    ")
	if err != nil {
		return "", err
	}
	return string(val), nil
}

func GetPageToDispatch(campaign Campaign) Page {
	var page Page

	for _, p := range campaign.Pages {
		if p.CycleHitsDone < p.CycleHitsTodo {
			page = p
			break
		}
	}

	return page
}

func ResetCampaignCycles(campaign Campaign) Campaign {
	sum := 0
	for i, p := range campaign.Pages {
		sum += p.CycleHitsDone
		if sum >= 100 {
			p.CycleHitsDone = 0
			campaign.Pages[i] = p
			sum = 0
		}
	}

	campaign.CyclesDone++

	return campaign
}

func UpdatePageCampaignCycles(ctx context.Context, campaign Campaign, page Page) Campaign {
	for i, p := range campaign.Pages {
		if p.ID == page.ID {
			page.CycleHitsDone++
			campaign.Pages[i] = page
			break
		}
	}

	return campaign
}

func (analytic *Analytic) addParam(param AnalyticParam) []AnalyticParam {
	analytic.Params = append(analytic.Params, param)
	return analytic.Params
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
		if len(pageUrlParams) > 0 {
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

	if len(pageUrlParams) > 0 {
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

func getDateTime() string {
	loc, _ := time.LoadLocation("Europe/Paris")
	return time.Now().In(loc).Format("2006-01-02 15:04:05")
}

func saveAnalytic(c echo.Context, ctx context.Context, rdb *redis.Client, page Page, t string) error {
	params := c.Request().URL.Query()

	analytic := Analytic{
		page.ID,
		getDateTime(),
		t,
		[]AnalyticParam{},
	}

	if len(params) > 0 {
		for k := range params {
			analytic.addParam(AnalyticParam{
				k,
				params.Get(k),
			})
		}
	}

	data, err := json.Marshal(analytic)

	if err != nil {
		return err
	}

	err = rdb.LPush(ctx, "tasks", data).Err()
	if err != nil {

		res, err := PrettyStruct(analytic)
		fmt.Println("----------------------------")
		fmt.Println("Analytic (hit) pushed to redis")
		fmt.Println("----------------------------")
		fmt.Println(res)

		return err
	}

	return nil
}

func getPage(campaign Campaign, rdb *redis.Client, ctx context.Context) Page {
	page := GetPageToDispatch(campaign)

	if page == (Page{}) {
		campaign = ResetCampaignCycles(campaign)
		page = GetPageToDispatch(campaign)
	}

	campaign = UpdatePageCampaignCycles(ctx, campaign, page)

	res, err := json.Marshal(campaign)
	err = rdb.Set(ctx, "campaign:"+campaign.Key, res, 0).Err()

	if err != nil {
		panic(err)
	}

	return page
}

// -------------------------------------------------------- //
//
//	HANDLERS
//
// -------------------------------------------------------- //
func tracking(c echo.Context) error {
	data, err := ioutil.ReadFile("assets/tracking.js")

	if err != nil {
		return c.String(http.StatusNotFound, "Not found")
	}

	output := bytes.Replace(data, []byte("{{APP_URL}}"), []byte(os.Getenv("HTTP_DOMAIN")), -1)

	return c.Blob(http.StatusOK, "application/javascript; charset=utf-8", output)
}

func writeLog(s string) {
	//token := "EFnv7f3xN8Hy2iFMcthRqHHU"
	token := os.Getenv("LOGTAIL_TOKEN") //"C64u6nYXMESahJ34gQMPBNW9"
	logger := logtail.NewLogtail(token).NewLogger()
	logger.Print(s)
}

func redirect(c echo.Context) error {
	key := c.Param("key")
	ctx := context.Background()

	//	GET PAGE FROM REDIS
	rdb := getRedisClient()
	val, err := rdb.Get(ctx, "campaign:"+key).Result()

	if err != nil {
		rdb.Close()
		ctx.Done()
		runtime.GC()

		return c.String(http.StatusOK, "Campaign not found")
	}

	campaign := Campaign{}
	json.Unmarshal([]byte(val), &campaign)

	//	COMPUTE PAGE & REFRESH REDIS
	page := getPage(campaign, rdb, ctx)

	//	PUSH ANALYTIC TO REDIS QUEUE
	saveAnalytic(c, ctx, rdb, page, "hit")

	rdb.Close()
	ctx.Done()
	runtime.GC()

	url := getPageUrl(c, page)

	writeLog("Input URL : " + c.Request().URL.String())
	writeLog("Output URL : " + url)

	return c.Redirect(302, url)
}

func root(c echo.Context) error {
	return c.JSON(http.StatusOK, struct{ Status string }{Status: "OK"})
}

func view(c echo.Context) error {
	key := c.FormValue("intoid")

	if key != "" {
		intoid, err := strconv.Atoi(key)

		ctx := context.Background()

		rdb := getRedisClient()

		analytic := &Analytic{
			intoid,
			getDateTime(),
			"view",
			[]AnalyticParam{},
		}

		params, _ := c.FormParams()

		for k := range params { // Loop and push each param to analytic struct
			analytic.addParam(AnalyticParam{
				k,
				params.Get(k),
			})
		}

		res, err := json.Marshal(analytic)
		err = rdb.LPush(ctx, "tasks", res).Err()

		if err != nil {

			res2, err := PrettyStruct(analytic)
			fmt.Println("----------------------------")
			fmt.Println("Analytic (view) pushed to redis")
			fmt.Println("----------------------------")
			fmt.Println(res2)

			if err != nil {
			}
			// panic(err)
		}

		analytic = nil

		rdb.Close()
		ctx.Done()
		runtime.GC()
	}

	return c.String(http.StatusOK, "POST !")
}

// -------------------------------------------------------- //
//
//	MAIN
//
// -------------------------------------------------------- //
func main() {

	debug.SetGCPercent(-1)
	//	debug.SetMemoryLimit(1)

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	e := echo.New()
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept},
	}))

	e.POST("/hooks/campaign/view", view)
	e.GET("/tracking.js", tracking)
	e.GET("/:key", redirect)
	e.GET("/", root)

	e.Start(":" + os.Getenv("HTTP_PORT"))
}
