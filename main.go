package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gedex/go-instagram/instagram"
	fb "github.com/huandu/facebook"
)

var name = flag.String("n", "", "Page name")
var numOfWorkersPtr = flag.Int("c", 2, "The number of concurrence workers")
var isFromFB = flag.Bool("f", true, "get image from FB or IG")
var outputPath = flag.String("o", "", "Output folder path")
var m sync.Mutex
var token string
var igClient *instagram.Client
var igClientID string
var fileIndex int = 0

func getFileIndex() (ret int) {
	m.Lock()
	ret = fileIndex
	fileIndex = fileIndex + 1
	m.Unlock()
	return ret
}

func selectSource(isFromFB bool) {
	if isFromFB {
		token = os.Getenv("FBTOKEN")
	} else {
		token = os.Getenv("IGTOKEN")
		igClientID = os.Getenv("InstagramID")
		if igClientID == "" {
			log.Fatalln("Please set InstagramID")
		}
	}
}

func downloadWorker(destDir string, linkChan chan DLData, wg *sync.WaitGroup) {
	defer wg.Done()

	for target := range linkChan {
		var imageType string
		if strings.Contains(target.ImageURL, ".png") {
			imageType = ".png"
		} else {
			imageType = ".jpg"
		}

		resp, err := http.Get(target.ImageURL)
		if err != nil {
			log.Println("Http.Get\nerror: " + err.Error() + "\ntarget: " + target.ImageURL)
			continue
		}

		defer resp.Body.Close()

		m, _, err := image.Decode(resp.Body)
		if err != nil {
			log.Println("image.Decode\nerror: " + err.Error() + "\ntarget: " + target.ImageURL)
			continue
		}

		bounds := m.Bounds()
		if bounds.Size().X > 300 && bounds.Size().Y > 300 {
			out, err := os.Create(destDir + "/" + target.ImageID + imageType)
			if err != nil {
				log.Println("os.Create\nerror: ", err)
				continue
			}

			defer out.Close()
			if imageType == ".png" {
				png.Encode(out, m)
			} else {
				jpeg.Encode(out, m, nil)
			}
		}
	}
}

func findFBPhotoByAlbum(ownerName string, albumName string, albumID string, baseDir string, photoCount int, photoOffset int) {
	photoRet := FBPhotos{}

	var queryString string
	if photoOffset > 0 {
		queryString = fmt.Sprintf("/%s/photos?limit=%d&offset=%d", albumID, photoCount, photoOffset)
	} else {
		queryString = fmt.Sprintf("/%s/photos?limit=%d", albumID, photoCount)
	}

	resPhoto := runFBGraphAPI(queryString)
	parseMapToStruct(resPhoto, &photoRet)
	dir := fmt.Sprintf("%v/%v/%v - %v", baseDir, ownerName, albumID, albumName)
	os.MkdirAll(dir, 0755)
	linkChan := make(chan DLData)
	wg := new(sync.WaitGroup)
	for i := 0; i < 1; i++ {
		wg.Add(1)
		go downloadWorker(dir, linkChan, wg)
	}
	for _, v := range photoRet.Data {
		dlChan := DLData{}
		dlChan.ImageID = v.ID
		dlChan.ImageURL = v.Source
		linkChan <- dlChan
	}
}

func findIGPhotos(ownerName string, albumName string, userID string, baseDir string) {
	totalPhotoNumber := 1
	var mediaList []instagram.Media
	var next *instagram.ResponsePagination
	var optParam *instagram.Parameters
	var err error

	dir := fmt.Sprintf("%v/%v", baseDir, ownerName)
	os.MkdirAll(dir, 0755)
	linkChan := make(chan DLData)

	wg := new(sync.WaitGroup)
	for i := 0; i < 1; i++ {
		wg.Add(1)
		go downloadWorker(dir, linkChan, wg)
	}

	for true {
		maxID := ""
		if next != nil {
			maxID = next.NextMaxID
		}

		optParam = &instagram.Parameters{Count: 10, MaxID: maxID}
		mediaList, next, err = igClient.Users.RecentMedia(userID, optParam)
		if err != nil {
			log.Println("err:", err)
			break
		}

		for _, media := range mediaList {
			totalPhotoNumber = totalPhotoNumber + 1
			dlChan := DLData{}
			dlChan.ImageID = strconv.Itoa(totalPhotoNumber)
			dlChan.ImageURL = media.Images.StandardResolution.URL
			linkChan <- dlChan
		}

		if len(mediaList) == 0 || next.NextMaxID == "" {
			break
		}
	}
}

func findPhotos(isFromFB bool, pageName string, baseDir string) {
	if isFromFB {
		resUser := runFBGraphAPI("/" + pageName)
		userRet := FBUser{}
		parseMapToStruct(resUser, &userRet)

		resAlbums := runFBGraphAPI("/" + pageName + "/albums")
		albumRet := FBAlbums{}
		parseMapToStruct(resAlbums, &albumRet)

		maxCount := 30

		for _, v := range albumRet.Data {
			fmt.Println("Starting download ["+v.Name+"]-"+v.From.Name, " total count: ", v.Count)

			if v.Count > maxCount {
				currentOffset := 0
				for {
					if currentOffset > v.Count {
						break
					}
					findFBPhotoByAlbum("PeterHo", v.Name, v.ID, baseDir, maxCount, currentOffset)
					currentOffset = currentOffset + maxCount
				}
			} else {
				findFBPhotoByAlbum("PeterHo", v.Name, v.ID, baseDir, v.Count, 0)
			}
		}
	} else {
		igClient = instagram.NewClient(nil)
		igClient.ClientID = igClientID

		var userID string
		searchUsers, _, err := igClient.Users.Search(pageName, nil)
		if err != nil {
			log.Fatalln("FB connect error, err=", err.Error())
		}

		for _, user := range searchUsers {
			if user.Username == pageName {
				userID = user.ID
			}
		}

		findIGPhotos("PeterHo", pageName, userID, baseDir)
	}
}

func parseMapToStruct(inData interface{}, decodeStruct interface{}) {
	jret, _ := json.Marshal(inData)
	err := json.Unmarshal(jret, &decodeStruct)

	if err != nil {
		log.Fatalln(err)
	}
}

func runFBGraphAPI(query string) (queryResult interface{}) {
	res, err := fb.Get(query, fb.Params{
		"access_token": token,
	})

	if err != nil {
		log.Fatalln("FB connect error, err=", err.Error())
	}

	return res
}

func main() {
	flag.Parse()
	if token == "" {
		log.Fatalln("Set your FB token as environment variables")
	}

	if *name == "" {
		log.Fatalln("You need to input -n=Name-or-id")
	}

	if *outputPath == "" {
		log.Fatalln("You need to input -o=Name-or-id")
	}

	selectSource(*isFromFB)

	pageName := *name
	baseDir := *outputPath

	findPhotos(*isFromFB, pageName, baseDir)
}
