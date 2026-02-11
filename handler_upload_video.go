package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

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
	defer tempFile.Close()
	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't copy to a temporary file", err)
		return
	}

	processedFilePath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't process video for fast start", err)
		return
	}

	tempFileProcessed, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't open processed video file", err)
		return
	}
	defer os.Remove(tempFileProcessed.Name())
	defer tempFileProcessed.Close()

	aspectRatio, err := getVideoAspectRatio(tempFileProcessed.Name())

	// Reset the tempFile's file pointer to the beginning with .Seek(0, io.SeekStart) - this will allow us to read the file again from the beginning
	// _, err = tempFile.Seek(0, io.SeekStart)
	// if err != nil {
	// 	respondWithError(w, http.StatusInternalServerError, "Could not reset the tempFile's file pointer to the beginning", err)
	// 	return
	// }

	// Put the object into S3 using PutObject. You'll need to provide:
	// The bucket name
	// The file key. Use the same <random-32-byte-hex>.ext format as the key. e.g. 1a2b3c4d5e6f7890abcd1234ef567890.mp4
	// The file contents (body). The temp file is an os.File which implements io.Reader
	// Content type, which is the MIME type of the file.
	key := make([]byte, 32)
	rand.Read(key)
	s3VideoName := base64.RawURLEncoding.EncodeToString(key)
	s3VideoNameWithExtension := fmt.Sprintf("%v/%v.%v", aspectRatio, s3VideoName, videoExtension)

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(s3VideoNameWithExtension),
		Body:        tempFileProcessed,
		ContentType: aws.String(mediatype),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not put video to the S3", err)
		return
	}

	// Update the VideoURL of the video record in the database with the S3 bucket and key. S3 URLs are in the format https://<bucket-name>.s3.<region>.amazonaws.com/<key>. Make sure you use the correct region and bucket name!
	// s3VideoUrl := fmt.Sprintf("https://%v.s3.%v.amazonaws.com/%v", cfg.s3Bucket, cfg.s3Region, s3VideoNameWithExtension)
	s3VideoUrl := fmt.Sprintf("%v,%v", cfg.s3Bucket, s3VideoNameWithExtension)
	video.VideoURL = &s3VideoUrl

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	video, err = cfg.dbVideoToSignedVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't generate video with the presigned URL", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)

	// Restart your server and test the handler by uploading the boots-video-vertical.mp4 file. Make sure that:
	// The video is correctly uploaded to your S3 bucket.
	// The video_url in your database is updated with the S3 bucket and key (and thus shows up in the web UI)

}

func processVideoForFastStart(filePath string) (string, error) {
	// Create a new string for the output file path. I just appended .processing to the input file (which should be the path to the temp file on disk)
	// Create a new exec.Cmd using exec.Command
	// The command is ffmpeg and the arguments are -i, the input file path, -c, copy, -movflags, faststart, -f, mp4 and the output file path.
	// Run the command
	// Return the output file path
	outputFilePath := fmt.Sprintf("%v.processing", filePath)

	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputFilePath)

	err := cmd.Run()
	if err != nil {
		return "", err
	}

	return outputFilePath, nil
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	// Use the SDK to create a s3.PresignClient with s3.NewPresignClient
	presignS3Client := s3.NewPresignClient(s3Client)
	// Use the client's .PresignGetObject() method with s3.WithPresignExpires as a functional option.
	presignedHttpReq, err := presignS3Client.PresignGetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", err
	}
	// Return the .URL field of the v4.PresignedHTTPRequest created by .PresignGetObject()
	return presignedHttpReq.URL, nil
}
