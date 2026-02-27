package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
)

func main() {
	url := "http://localhost:8080/api/admin/approve-validation"
	payload := []byte(`{"id": 4, "adminId": 0}`)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Authorization", "Bearer LA_MIA_CHIAVE_SEGRETA_123")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)
	fmt.Println("Status:", resp.Status)
	fmt.Println("Body:", string(body))
}
