package client

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"golang.org/x/net/context"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const BaseURL = "https://api.dns.constellix.com/"

type Client struct {
	httpclient  *http.Client
	rateLimiter *rate.Limiter // Optional
	apiKey      string        // Required
	secretKey   string        // Required
	insecure    bool          // Optional
	proxyUrl    string        // Optional
}

//singleton implementation of a client
var clientImpl *Client

type Option func(*Client)

func Insecure(insecure bool) Option {
	return func(client *Client) {
		client.insecure = insecure
	}
}

func ProxyUrl(pUrl string) Option {
	return func(client *Client) {
		client.proxyUrl = pUrl
	}
}

func RequestInterval(pInterval float32) Option {
	return func(client *Client) {
		if pInterval >= 0 {
			rl := rate.NewLimiter(rate.Every(time.Duration(pInterval*1000)), 1)
			client.rateLimiter = rl
		}
	}
}

func initClient(apiKey, secretKey string, options ...Option) *Client {
	//existing information about client
	client := &Client{
		apiKey:    apiKey,
		secretKey: secretKey,
	}
	for _, option := range options {
		option(client)
	}

	//Setting up the HTTP client for the API call
	var transport *http.Transport
	transport = client.useInsecureHTTPClient(client.insecure)
	if client.proxyUrl != "" {
		transport = client.configProxy(transport)
	}
	client.httpclient = &http.Client{
		Transport: transport,
	}
	return client
}

//Returns a singleton
func GetClient(apiKey, secretKey string, options ...Option) *Client {
	clientImpl = initClient(apiKey, secretKey, options...)
	return clientImpl
}

func (c *Client) useInsecureHTTPClient(insecure bool) *http.Transport {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			CipherSuites: []uint16{
				tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			},
			PreferServerCipherSuites: true,
			InsecureSkipVerify:       insecure,
			MinVersion:               tls.VersionTLS11,
			MaxVersion:               tls.VersionTLS12,
		},
	}

	return transport
}

func (c *Client) configProxy(transport *http.Transport) *http.Transport {
	pUrl, err := url.Parse(c.proxyUrl)
	if err != nil {
		log.Fatal(err)
	}
	transport.Proxy = http.ProxyURL(pUrl)
	return transport
}

func getToken(apiKey, secretKey string) string {
	// Extracts epoch time in milliseconds
	epochTime := strconv.FormatInt(time.Now().UnixNano()/int64(time.Millisecond), 10)

	// Calculate hmac using secret key and epoch time
	h := hmac.New(sha1.New, []byte(secretKey))
	h.Write([]byte(epochTime))
	sha := base64.StdEncoding.EncodeToString(h.Sum(nil))

	// Building token as 'apikey:hmac:time'
	token := apiKey + ":" + sha + ":" + epochTime
	return token
}

func (c *Client) makeRequest(method, endpoint string, payload []byte) (*http.Request, error) {
	//Defining http request
	var req *http.Request
	var err error
	if method == "POST" || method == "PUT" {
		req, err = http.NewRequest(method, endpoint, bytes.NewBuffer(payload))
	} else {
		req, err = http.NewRequest(method, endpoint, nil)
	}
	if err != nil {
		return nil, err
	}

	//Calling for token and setting headers
	token := getToken(c.apiKey, c.secretKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-cns-security-token", token)

	return req, nil
}

func (c *Client) Save(obj interface{}, endpoint string) (responce *http.Response, err error) {
	jsonPayload, err := json.Marshal(obj)
	if err != nil {
		log.Fatal(err)
	}

	urlArr := strings.Split(endpoint, "/")
	var url string
	var flag bool
	if len(urlArr) > 2 && urlArr[2] == "api.sonar.constellix.com" {
		url = endpoint
		flag = true
	} else {
		url = fmt.Sprintf("%s%s", BaseURL, endpoint)
	}

	req, err1 := c.makeRequest("POST", url, jsonPayload)
	log.Println(req)
	if err1 != nil {
		return nil, err1
	}

	ctx := context.Background()
	err = c.rateLimiter.Wait(ctx) // This is a blocking call. Honors the rate limit
	if err != nil {
		return nil, err
	}

	resp, err2 := c.httpclient.Do(req)
	if err2 != nil {
		return nil, err2
	}
	log.Println(resp)
	if flag == false {
		return resp, checkForErrors(resp)
	}
	return resp, checkForErrorsChecks(resp)
}

func checkForErrors(resp *http.Response) error {
	if resp.StatusCode != http.StatusOK {
		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatal(err)
		}
		bodyString := string(bodyBytes)

		var data map[string]interface{}
		_ = json.Unmarshal([]byte(bodyString), &data)

		var errors []interface{}
		errors = data["errors"].([]interface{})

		var allErrs string
		for _, val := range errors {
			allErrs = allErrs + val.(string)
		}
		log.Println(" Errors are .....:: ", allErrs)
		return fmt.Errorf("%s", allErrs)
	}
	return nil
}

func checkForErrorsChecks(resp *http.Response) error {
	if resp.StatusCode != http.StatusOK && resp.StatusCode != 201 && resp.StatusCode != 202 {
		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatal(err)
		}
		bodyString := string(bodyBytes)

		return fmt.Errorf("%s", bodyString)
	}
	return nil
}

func (c *Client) GetbyId(endpoint string) (response *http.Response, err error) {
	var url string
	var flag bool
	urlArr := strings.Split(endpoint, "/")
	if len(urlArr) > 2 && urlArr[2] == "api.sonar.constellix.com" {
		url = endpoint
		flag = true
	} else {
		url = fmt.Sprintf("%s%s", BaseURL, endpoint)
	}

	req, err := c.makeRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	log.Println("In GET by ID :", req)

	if c.rateLimiter != nil {
		ctx := context.Background()
		err = c.rateLimiter.Wait(ctx) // This is a blocking call. Honors the rate limit
		if err != nil {
			return nil, err
		}
	}

	resp, err1 := c.httpclient.Do(req)
	if err1 != nil {
		return nil, err1
	}

	log.Println("Response for Get: ", resp)
	if flag == false {
		return resp, checkForErrors(resp)
	}
	return resp, checkForErrorsChecks(resp)
}

func (c *Client) DeletebyId(endpoint string) error {
	var url string
	urlArr := strings.Split(endpoint, "/")
	if len(urlArr) > 2 && urlArr[2] == "api.sonar.constellix.com" {
		url = endpoint
	} else {
		url = fmt.Sprintf("%s%s", BaseURL, endpoint)
	}

	req, err := c.makeRequest("DELETE", url, nil)
	if err != nil {
		return err
	}

	log.Println("request for delete : ", req)

	if c.rateLimiter != nil {
		ctx := context.Background()
		err = c.rateLimiter.Wait(ctx) // This is a blocking call. Honors the rate limit
		if err != nil {
			return err
		}
	}

	resp, err1 := c.httpclient.Do(req)
	if err1 != nil {
		log.Println("Response from server for delete : ", resp)
		return err1
	}
	log.Println("Response from server for delete : ", resp)
	return checkForErrorsChecks(resp)
}

func (c *Client) UpdatebyID(obj interface{}, endpoint string) (response *http.Response, err error) {
	jsonPayload, err := json.Marshal(obj)
	if err != nil {
		log.Fatal(err)
	}
	urlArr := strings.Split(endpoint, "/")
	var url string
	var flag bool
	if len(urlArr) > 2 && urlArr[2] == "api.sonar.constellix.com" {
		url = endpoint
		flag = true
	} else {
		url = fmt.Sprintf("%s%s", BaseURL, endpoint)
	}

	req, err1 := c.makeRequest("PUT", url, jsonPayload)
	log.Println(req)
	if err1 != nil {
		return nil, err1
	}

	if c.rateLimiter != nil {
		ctx := context.Background()
		err = c.rateLimiter.Wait(ctx) // This is a blocking call. Honors the rate limit
		if err != nil {
			return nil, err
		}
	}

	resp, err2 := c.httpclient.Do(req)
	if err2 != nil {
		return nil, err2
	}
	log.Println(resp)
	if flag == false {
		return resp, checkForErrors(resp)
	}
	return resp, checkForErrorsChecks(resp)
}
