package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
)

func main() {
	loginPayload := []byte(`{"employeeId": 15, "pin": "PIN_ADMIN"}`)
	loginReq, err := http.NewRequest("POST", "http://localhost:8080/api/admin/login", bytes.NewBuffer(loginPayload))
	if err != nil {
		panic(err)
	}
	loginReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	loginResp, err := client.Do(loginReq)
	if err != nil {
		panic(err)
	}
	defer loginResp.Body.Close()

	loginBody, _ := ioutil.ReadAll(loginResp.Body)
	if loginResp.StatusCode != http.StatusOK {
		fmt.Println("Login status:", loginResp.Status)
		fmt.Println("Login body:", string(loginBody))
		return
	}

	var loginData struct {
		Token  string `json:"token"`
		APIKey string `json:"apiKey"`
	}
	if err := json.Unmarshal(loginBody, &loginData); err != nil {
		panic(err)
	}
	token := loginData.Token
	if token == "" {
		token = loginData.APIKey
	}

	url := "http://localhost:8080/api/admin/approve-validation"
	payload := []byte(`{"id": 4}`)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)
	fmt.Println("Status:", resp.Status)
	fmt.Println("Body:", string(body))
}
