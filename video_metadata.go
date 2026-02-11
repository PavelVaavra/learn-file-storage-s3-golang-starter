package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
)

func getVideoAspectRatio(filePath string) (string, error) {
	// It should use exec.Command to run the same ffprobe command as above. In this case, the command is ffprobe and the arguments are -v, error,
	// -print_format, json, -show_streams, and the file path.
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	// Set the resulting exec.Cmd's Stdout field to a pointer to a new bytes.Buffer.
	var b bytes.Buffer
	cmd.Stdout = &b

	// .Run() the command
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	// Unmarshal the stdout of the command from the buffer's .Bytes into a JSON struct so that you can get the width and height fields.
	var result map[string][]map[string]interface{}

	err = json.Unmarshal(b.Bytes(), &result)
	if err != nil {
		return "", err
	}

	width := int(result["streams"][0]["width"].(float64))
	height := int(result["streams"][0]["height"].(float64))

	// I did a bit of math to determine the ratio, then returned one of three strings: 16:9, 9:16, or other.
	if width > height {
		if width/16 == height/9 {
			return "landscape", nil
		}
	}
	if width/9 == height/16 {
		return "portrait", nil
	}

	return "other", nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	// It should take a video database.Video as input and return a database.Video with the VideoURL field set to a presigned URL and an error
	// (to be returned from the handler)
	// It should first split the video.VideoURL on the comma to get the bucket and key
	// fmt.Println(&video.VideoURL)
	if video.VideoURL == nil {
		return video, nil
	}
	parts := strings.Split(*video.VideoURL, ",")
	bucket, key := parts[0], parts[1]
	// Then it should use generatePresignedURL to get a presigned URL for the video
	presignedUrl, err := generatePresignedURL(cfg.s3Client, bucket, key, time.Hour)
	if err != nil {
		return video, err
	}
	// Set the VideoURL field of the video to the presigned URL and return the updated video
	video.VideoURL = &presignedUrl
	return video, nil
}
