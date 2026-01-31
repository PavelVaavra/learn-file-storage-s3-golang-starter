package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	// Set an upload limit of 1 GB (1 << 30 bytes) using http.MaxBytesReader.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)

	// Extract the videoID from the URL path parameters and parse it as a UUID
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	// Authenticate the user to get a userID
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading video", videoID, "by user", userID)

	// Get the video metadata from the database, if the user is not the video owner, return a http.StatusUnauthorized response
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't find video", err)
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Not authorized to upload this video", nil)
		return
	}

	// Parse the uploaded video file from the form data
	// Use (http.Request).FormFile with the key "video" to get a multipart.File in memory
	// Remember to defer closing the file with (os.File).Close - we don't want any memory leaks
	// const maxMemory = 1 << 30
	// r.ParseMultipartForm(maxMemory)
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	// Validate the uploaded file to ensure it's an MP4 video
	// Use mime.ParseMediaType and "video/mp4" as the MIME type
	contentType := header.Header.Get("Content-Type")
	mediatype, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to parse media type", err)
		return
	}
	if mediatype != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Only mp4 video can be uploaded", err)
		return
	}
	videoExtension := strings.Split(contentType, "/")[1]

	// Save the uploaded file to a temporary file on disk.
	// Use os.CreateTemp to create a temporary file. I passed in an empty string for the directory to use the system default, and the name "tubely-upload.mp4" (but you can use whatever you want)
	// defer remove the temp file with os.Remove
	// defer close the temp file (defer is LIFO, so it will close before the remove)
	// io.Copy the contents over from the wire to the temp file
	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not create temporary file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer file.Close()
	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't copy to a temporary file", err)
		return
	}

	// Reset the tempFile's file pointer to the beginning with .Seek(0, io.SeekStart) - this will allow us to read the file again from the beginning
	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not reset the tempFile's file pointer to the beginning", err)
		return
	}

	// Put the object into S3 using PutObject. You'll need to provide:
	// The bucket name
	// The file key. Use the same <random-32-byte-hex>.ext format as the key. e.g. 1a2b3c4d5e6f7890abcd1234ef567890.mp4
	// The file contents (body). The temp file is an os.File which implements io.Reader
	// Content type, which is the MIME type of the file.
	key := make([]byte, 32)
	rand.Read(key)
	s3VideoName := base64.RawURLEncoding.EncodeToString(key)
	s3VideoNameWithExtension := fmt.Sprintf("%v.%v", s3VideoName, videoExtension)

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(s3VideoNameWithExtension),
		Body:        tempFile,
		ContentType: aws.String(mediatype),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not put video to the S3", err)
		return
	}

	// Update the VideoURL of the video record in the database with the S3 bucket and key. S3 URLs are in the format https://<bucket-name>.s3.<region>.amazonaws.com/<key>. Make sure you use the correct region and bucket name!
	s3VideoUrl := fmt.Sprintf("https://%v.s3.%v.amazonaws.com/%v", cfg.s3Bucket, cfg.s3Region, s3VideoNameWithExtension)
	video.VideoURL = &s3VideoUrl

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)

	// Restart your server and test the handler by uploading the boots-video-vertical.mp4 file. Make sure that:
	// The video is correctly uploaded to your S3 bucket.
	// The video_url in your database is updated with the S3 bucket and key (and thus shows up in the web UI)

}
