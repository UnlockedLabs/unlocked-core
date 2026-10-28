package providerplatforms

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
)

func stripUuidForUrl(uuid string) string {
	return strings.ReplaceAll(uuid, "-", "")
}

func (kc *KolibriService) IntoCourse(data map[string]interface{}) *Course {
	courseType := data["course_type"].(string)
	thumbnail := data["thumbnail"].(string)
	root := data["root_id"].(string)
	id := data["id"].(string)
	name := data["name"].(string)
	description := data["description"].(string)
	if len(description) > 255 {
		description = description[:255]
	}
	totalResourceCount := data["total_resource_count"].(int64)
	course := Course{
		ProviderPlatformID:      kc.ProviderPlatformID,
		ExternalID:              id,
		Name:                    name,
		Type:                    "open_content",
		OutcomeTypes:            stringPtr("completion"),
		TotalProgressMilestones: uint(totalResourceCount),
	}

	if courseType == "channel" {
		imgUrl, err := UploadImage(thumbnail, root, id)
		if err != nil {
			log.Printf("Failed to upload image %v", err)
			imgUrl = ""
		}
		course.Description = nilIfBlank(description)
		course.ThumbnailURL = nilIfBlank(imgUrl)
		if externalUrl, err := url.JoinPath(kc.BaseURL, "en/learn/#/topics/t/", stripUuidForUrl(id), "/folders?last=HOME"); err == nil {
			course.ExternalURL = stringPtr(externalUrl)
		}
	} else {
		course.Description = stringPtr("Kolibri managed course")
		if externUrl, err := url.JoinPath(kc.BaseURL, "/en/learn/#/home/classes/", stripUuidForUrl(id)); err == nil {
			course.ExternalURL = stringPtr(externUrl)
		} else {
			course.ExternalURL = stringPtr(kc.BaseURL + "/en/learn/#/home/classes/" + stripUuidForUrl(id))
		}
	}
	return &course
}

func decodeImg(thumbnail string) []byte {
	if strings.Contains(thumbnail, "data:image/png;base64") {
		img := strings.Split(thumbnail, ",")[1]
		imgDec, _ := base64.StdEncoding.DecodeString(img)
		return imgDec
	}
	return nil
}

func UploadImage(thumbnail, root, id string) (string, error) {
	log.Println("Uploading image")
	imgData := decodeImg(thumbnail)
	if imgData == nil {
		return "", errors.New("no image data available or decoding failed")
	}
	filename := "image_" + root + "/" + id + ".png"
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err = part.Write(imgData); err != nil {
		return "", err
	}
	err = writer.Close()
	if err != nil {
		return "", err
	}
	request, err := http.NewRequest("POST", os.Getenv("APP_URL")+"/upload", body)
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Content-Length", fmt.Sprintf("%d", len(body.Bytes())))
	client := &http.Client{}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() {
		if response.Body.Close() != nil {
			log.Error("Failed to close response body")
		}
	}()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned non-OK status: %s", response.Status)
	}
	urlRes := struct {
		Data struct {
			Url string `json:"url"`
		}
		Message string `json:"message"`
	}{}
	err = json.NewDecoder(response.Body).Decode(&urlRes)
	if err != nil {
		return "", err
	}
	return urlRes.Data.Url, nil
}
