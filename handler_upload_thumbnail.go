package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here
	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	// Get the image data from the form
	// Use r.FormFile to get the file data and file headers. The key the web browser is using is called "thumbnail"
	// Get the media type from the form file's Content-Type header
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()
	contentType := header.Header.Get("Content-Type")
	mediatype, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to parse media type", err)
		return
	}
	if mediatype != "image/jpeg" && mediatype != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Only jpeg or png can uploaded as thumbnails", err)
		return
	}
	fileExtension := strings.Split(contentType, "/")[1]

	// Get the video's metadata from the SQLite database. The apiConfig's db has a GetVideo method you can use
	// If the authenticated user is not the video owner, return a http.StatusUnauthorized response
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't find video", err)
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Not authorized to update this video", nil)
		return
	}

	// Update the video metadata so that it has a new thumbnail URL, then update the record in the database by using the cfg.db.UpdateVideo function.
	// The thumbnail URL should have this format:
	// http://localhost:<port>/api/thumbnails/{videoID}
	url := fmt.Sprintf("http://localhost:%v/api/thumbnails/%v", cfg.port, videoID)
	video.ThumbnailURL = &url

	// Instead of encoding to base64, update the handler to save the bytes to a file at the path /assets/<videoID>.<file_extension>.
	// 	Use the Content-Type header to determine the file extension.
	// 	Use the videoID to create a unique file path. filepath.Join and cfg.assetsRoot will be helpful here.
	// 	Use os.Create to create the new file
	// 	Copy the contents from the multipart.File to the new file on disk using io.Copy

	thumbnailName := fmt.Sprintf("%v.%v", videoID, fileExtension)
	thumbnailPath := filepath.Join(cfg.assetsRoot, thumbnailName)
	thumbnailFile, err := os.Create(thumbnailPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create a thumbnail file", err)
		return
	}
	defer thumbnailFile.Close()
	_, err = io.Copy(thumbnailFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't copy to a thumbnail file", err)
		return
	}

	thumbnailUrl := fmt.Sprintf("http://localhost:%v/%v", cfg.port, thumbnailPath)
	video.ThumbnailURL = &thumbnailUrl

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	// Respond with updated JSON of the video's metadata. Use the provided respondWithJSON function and pass it the updated database.Video
	// struct to marshal.

	respondWithJSON(w, http.StatusOK, video)
}
