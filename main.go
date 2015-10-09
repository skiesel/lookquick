package main

import (
	"appengine"
	"appengine/datastore"
	"appengine/memcache"
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"math/rand"
	"net/http"
	"strings"
	"text/template"
	"time"
)

var pages *template.Template
var keyRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")

type RawImage struct {
	Image      []byte
	ID         string
	Expiration time.Time
}

func (r *RawImage) ToRenderableString() string {
	return base64.StdEncoding.EncodeToString(r.Image)
}

func init() {
	pages = template.Must(template.ParseGlob("html/*.html"))

	http.HandleFunc("/", renderRequest)
	http.HandleFunc("/post", postRequest)
}

func postRequest(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)

	formImage, imageType, err := r.FormFile("file")

	if err != nil {
		renderError(w, c, err)
		return
	}

	var rawImage image.Image

	if strings.Contains(imageType.Filename, ".png") {
		rawImage, err = png.Decode(formImage)
	} else if strings.Contains(imageType.Filename, ".jpg") || strings.Contains(imageType.Filename, ".jpeg") {
		rawImage, err = jpeg.Decode(formImage)
	} else {
		rawImage, _, err = image.Decode(formImage)
	}

	if err != nil {
		renderError(w, c, err)
		return
	}

	buf := new(bytes.Buffer)
	err = jpeg.Encode(buf, rawImage, nil)

	key := randomKey(50)

	image := RawImage{
		Image:      buf.Bytes(),
		ID:         key,
		Expiration: time.Now().Add(time.Duration(5) * time.Minute),
	}

	dsKey := datastore.NewKey(c, "RawImage", key, 0, nil)

	_, err = datastore.Put(c, dsKey, &image)
	if err != nil {
		renderError(w, c, err)
		return
	}

	item := &memcache.Item{
		Key:    key,
		Object: image,
	}

	memcache.JSON.Set(c, item)

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(key))
}

func renderRequest(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()

	key, found := values["key"]

	c := appengine.NewContext(r)

	if !found || len(key) != 1 || key[0] == "" {
		err := pages.ExecuteTemplate(w, "index.html", nil)
		if err != nil {
			renderError(w, c, err)
			return
		}
		return
	}

	image, expired := getFromMemCache(c, key[0])
	if image == nil {
		if !expired {
			image, expired = getFromDatastore(c, key[0])
		}
		if image == nil || expired {
			err := pages.ExecuteTemplate(w, "notfound.html", key[0])
			if err != nil {
				renderError(w, c, err)
				return
			}
			return
		}
	}

	err := pages.ExecuteTemplate(w, "image.html", image)
	if err != nil {
		renderError(w, c, err)
	}
}

func renderError(w http.ResponseWriter, c appengine.Context, err error) {
	http.Error(w, "Failed to render the page.", http.StatusInternalServerError)
	c.Errorf("failed to render: %v", err)
}

func randomKey(size int64) string {
	runCount := len(keyRunes)
	key := make([]rune, size)
	for i := range key {
		key[i] = keyRunes[rand.Intn(runCount)]
	}
	return string(key)
}

func getFromMemCache(c appengine.Context, key string) (*RawImage, bool) {
	var image RawImage
	_, err := memcache.JSON.Get(c, key, &image)
	if err == memcache.ErrCacheMiss {
		return nil, false
	} else if err != nil {
		return nil, false
	}

	if image.Expiration.Before(time.Now()) {
		removeFromMemCache(c, key)
		removeFromDatastore(c, key, nil)
		return nil, true
	}

	return &image, false
}

func getFromDatastore(c appengine.Context, key string) (*RawImage, bool) {
	images := []*RawImage{}
	keys, err := datastore.NewQuery("RawImage").
		Filter("ID = ", key).
		GetAll(c, &images)

	if err != nil || len(images) == 0 {
		return nil, false
	}

	if images[0].Expiration.Before(time.Now()) {
		removeFromDatastore(c, key, keys[0])
		return nil, true
	}

	return images[0], false
}

func removeFromMemCache(c appengine.Context, key string) {
	memcache.Delete(c, key)
}

func removeFromDatastore(c appengine.Context, key string, dsKey *datastore.Key) {
	if dsKey == nil {
		images := []*RawImage{}
		keys, err := datastore.NewQuery("RawImage").
			Filter("ID = ", key).
			GetAll(c, &images)

		if err != nil || len(keys) == 0 {
			return
		}
		dsKey = keys[0]
	}

	datastore.Delete(c, dsKey)
}
