package webcontroller

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/its-a-feature/Mythic/database"
	databaseStructs "github.com/its-a-feature/Mythic/database/structs"
	"github.com/its-a-feature/Mythic/logging"
	"github.com/its-a-feature/Mythic/rabbitmq"
)

type C2HostFileMessageInput struct {
	Input C2HostFileMessage `json:"input" binding:"required"`
}

type C2HostFileMessage struct {
	C2ProfileID int    `json:"c2_id" binding:"required"`
	FileUUID    string `json:"file_uuid" binding:"required"`
	HostURL     string `json:"host_url" binding:"required"`
}

type C2HostFileMessageResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

func C2HostFileMessageWebhook(c *gin.Context) {
	// get variables from the POST request
	var input C2HostFileMessageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		logging.LogError(err, "Failed to parse out required parameters")
		c.JSON(http.StatusOK, C2HostFileMessageResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	}
	c2Profile := databaseStructs.C2profile{ID: input.Input.C2ProfileID}
	if err := database.DB.Get(&c2Profile, `SELECT "name" FROM c2profile WHERE id=$1`,
		input.Input.C2ProfileID); err != nil {
		logging.LogError(err, "Failed to find c2 profile")
		c.JSON(http.StatusOK, C2HostFileMessageResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	}
	hostFile := databaseStructs.Filemeta{}
	if err := database.DB.Get(&hostFile, `SELECT deleted FROM filemeta WHERE agent_file_id=$1`,
		input.Input.FileUUID); err != nil {
		logging.LogError(err, "Failed to find file")
		c.JSON(http.StatusOK, C2HostFileMessageResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	} else if hostFile.Deleted {
		c.JSON(http.StatusOK, C2HostFileMessageResponse{
			Status: "error",
			Error:  "File is deleted, can't be hosted",
		})
		return
	}
	c2HostFileResponse, err := rabbitmq.RabbitMQConnection.SendC2RPCHostFile(rabbitmq.C2HostFileMessage{
		Name:     c2Profile.Name,
		FileUUID: input.Input.FileUUID,
		HostURL:  input.Input.HostURL,
	})
	if err != nil {
		logging.LogError(err, "Failed to send RPC call to c2 profile in C2ProfileHostFileWebhook", "c2_profile", c2Profile.Name)
		c.JSON(http.StatusOK, C2HostFileMessageResponse{
			Status: "error",
			Error:  "Failed to send RPC message to c2 profile",
		})
		return
	}
	if !c2HostFileResponse.Success {
		c.JSON(http.StatusOK, C2HostFileMessageResponse{
			Status: "error",
			Error:  c2HostFileResponse.Error,
		})
		return
	}
	go func() {
		go rabbitmq.SendAllOperationsMessage("Stopping C2 Profile after hosting new file...", 0, "host_file", database.MESSAGE_LEVEL_INFO)
		stopC2ProfileResponse, err := rabbitmq.RabbitMQConnection.SendC2RPCStopServer(rabbitmq.C2StopServerMessage{
			Name: c2Profile.Name,
		})
		if err != nil {
			logging.LogError(err, "Failed to send RPC call to c2 profile in C2HostFileMessageWebhook", "c2_profile", c2Profile.Name)
			go rabbitmq.SendAllOperationsMessage("Failed to stop c2 profile after hosting file", 0, "host_file", database.MESSAGE_LEVEL_WARNING)
			return
		}
		if !stopC2ProfileResponse.Success {
			go rabbitmq.SendAllOperationsMessage(stopC2ProfileResponse.Error, 0, "", database.MESSAGE_LEVEL_WARNING)
			return
		}
		go rabbitmq.SendAllOperationsMessage("Starting C2 Profile after hosting new file...", 0, "host_file", database.MESSAGE_LEVEL_INFO)
		startC2ProfileResponse, err := rabbitmq.RabbitMQConnection.SendC2RPCStartServer(rabbitmq.C2StartServerMessage{
			Name: c2Profile.Name,
		})
		if err != nil {
			logging.LogError(err, "Failed to send RPC call to c2 profile in C2HostFileMessageWebhook", "c2_profile", c2Profile.Name)
			go rabbitmq.SendAllOperationsMessage("Failed to start c2 profile after hosting file", 0, "", database.MESSAGE_LEVEL_WARNING)
			return
		}
		if !startC2ProfileResponse.Success {
			go rabbitmq.SendAllOperationsMessage(startC2ProfileResponse.Error, 0, "", database.MESSAGE_LEVEL_WARNING)
			return
		}
		go rabbitmq.SendAllOperationsMessage("Successfully restarted C2 Profile after hosting a file", 0, "host_file", database.MESSAGE_LEVEL_INFO)
	}()
	c.JSON(http.StatusOK, C2HostFileMessageResponse{
		Status: "success",
		Error:  "",
	})
	return
}
