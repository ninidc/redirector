package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
	Traffic       string `json:"Traffic"`
	CycleHitsDone int    `json:"CycleHitsDone"`
	CycleHitsTodo int    `json:"CycleHitsTodo"`
}

type Stat struct {
	CampaignPageID int
	Date           string
	Type           string
	Param          string
}

// -------------------------------------------------------- //
//
//	FUNCTIONS
//
// -------------------------------------------------------- //
func getPage(campaign Campaign) Page {
	page := Page{}

	for _, p := range campaign.Pages {
		if p.CycleHitsDone < p.CycleHitsTodo {
			page = p
		}
	}

	return page
}

func resetPages(campaign Campaign) Campaign {
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

func updateCampaign(campaign Campaign, page Page) Campaign {
	for i, p := range campaign.Pages {
		if p.ID == page.ID {
			page.CycleHitsDone++
			campaign.Pages[i] = page
		}
	}

	return campaign
}

// -------------------------------------------------------- //
//
//	MAIN
//
// -------------------------------------------------------- //
func main() {
	e := echo.New()

	// ------------------------------------------ //
	//	CAMPAIGN
	// ------------------------------------------ //
	e.GET("/:key", func(c echo.Context) error {
		key := c.Param("key")
		param := c.QueryParam("pubfeed")
		ctx := context.Background()

		// ------------------------------------------ //
		//	GET & PAGE FROM REDIS
		// ------------------------------------------ //
		rdb := redis.NewClient(&redis.Options{
			Addr:     os.Getenv("REDIS_HOST") + ":" + os.Getenv("REDIS_PORT"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       0,
		})
		val, err := rdb.Get(ctx, "campaign:"+key).Result()
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
		page := getPage(campaign)

		if page == (Page{}) {
			campaign = resetPages(campaign)
			page = getPage(campaign)
		}

		campaign = updateCampaign(campaign, page)

		campaignJson, err := json.Marshal(campaign)
		err = rdb.Set(ctx, "campaign:"+key, campaignJson, 0).Err()

		if err != nil {
			panic(err)
		}
		// ------------------------------------------ //

		// ------------------------------------------ //
		//	PUSH STATS TO REDIS QUEUE
		// ------------------------------------------ //
		currentTime := time.Now()
		stat := Stat{
			page.ID,
			currentTime.Format("2006-01-02"),
			"hit",
			param,
		}
		statJson, err := json.Marshal(stat)
		err = rdb.LPush(ctx, "tasks", statJson).Err()
		if err != nil {
			panic(err)
		}
		// ------------------------------------------ //

		return c.String(http.StatusOK, page.URL)
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
