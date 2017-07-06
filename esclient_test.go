package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"encoding/json"

	"github.com/stretchr/testify/assert"
)

const TwoIndicesResponse = `
	[
		{
			"health": "yellow",
			"status": "open",
			"index": "idx0",
			"uuid": "sGD-VQy5StS1jIUbuo3R7A",
			"pri": "1",
			"rep": "1",
			"docs.count": "353400",
			"docs.deleted": "0",
			"store.size": "37.9mb",
			"pri.store.size": "37.9mb"
		},
		{
			"health": "yellow",
			"status": "open",
			"index": "idx1",
			"uuid": "DoA20IojS72IdaFSN8CX9Q",
			"pri": "1",
			"rep": "1",
			"docs.count": "38771",
			"docs.deleted": "0",
			"store.size": "11.2mb",
			"pri.store.size": "11.2mb"
		}
	]`

const IndexCreatedResponse = `
	{
		"acknowledged" : true,
		"shards_acknowledged" : true
	}`

const IndexAlreadyExistsResponse = `
	{
		"error" : {
			"root_cause" : [
				{
					"type" : "index_already_exists_exception",
					"reason" : "index [idx1/DoA20IojS72IdaFSN8CX9Q] already exists",
					"index_uuid" : "DoA20IojS72IdaFSN8CX9Q",
					"index" : "idx1"
				}
			],
			"type" : "index_already_exists_exception",
			"reason" : "index [idx1/DoA20IojS72IdaFSN8CX9Q] already exists",
			"index_uuid" : "DoA20IojS72IdaFSN8CX9Q",
			"index" : "idx1"
		},
		"status" : 400
	}`

const IndexDeletedResponse = `
	{
		"acknowledged" : true
	}`

const IndexNotFoundResponse = `
	{
		"error" : {
			"root_cause" : [
			{
				"type" : "index_not_found_exception",
				"reason" : "no such index",
				"resource.type" : "index_or_alias",
				"resource.id" : "idx1",
				"index_uuid" : "_na_",
				"index" : "idx1"
			}
			],
			"type" : "index_not_found_exception",
			"reason" : "no such index",
			"resource.type" : "index_or_alias",
			"resource.id" : "idx1",
			"index_uuid" : "_na_",
			"index" : "idx1"
		},
		"status" : 404
	}`

const LaunchWithoutTestItems = `
	{
		"launchId": "1234567890",
		"launchName": "Launch without test items",
		"testItems": []
	}`

const LaunchWithTestItemsWithoutLogs = `
	{
		"launchId": "1234567891",
		"launchName": "Launch with test items without logs",
		"testItems": [
			{
				"testItemId": "0001",
				"issueType": "TI001",
				"logs": []
			}
		]
	}`

const LaunchWithTestItemsWithLogs = `
	{
		"launchId": "1234567892",
		"launchName": "Launch with test items with logs",
		"testItems": [
			{
				"testItemId": "0002",
				"issueType": "TI001",
				"logs": [
					{
						"logId": "0001",
						"logLevel": 40000,
						"message": "Message 1"
					}
				]
			}
		]
	}`

const LogIndexRequest = `{"index":{"_id":"0001","_index":"idx2","_type":"log"}}
{"issue_type":"TI001","launch_name":"Launch with test items with logs","log_level":40000,"message":"Message ","test_item":"0002"}
`

const LogIndexResponse = `
	{
		"took" : 63,
		"errors" : false,
		"items" : [
			{
				"index" : {
					"_index" : "idx2",
					"_type" : "log",
					"_id" : "0001",
					"_version" : 1,
					"result" : "created",
					"_shards" : {
						"total" : 2,
						"successful" : 1,
						"failed" : 0
					},
					"created" : true,
					"status" : 201
				}
			}
		]
	}`

func TestListIndices(t *testing.T) {
	tests := []struct {
		params        map[string]interface{}
		expectedCount int
		expectErr     bool
	}{
		{
			params: map[string]interface{}{
				"statusCode": http.StatusOK,
				"response":   "[]",
			},
			expectedCount: 0,
			expectErr:     false,
		},
		{
			params: map[string]interface{}{
				"statusCode": http.StatusOK,
				"response":   TwoIndicesResponse,
			},
			expectedCount: 2,
			expectErr:     false,
		},
		{
			params: map[string]interface{}{
				"statusCode": http.StatusInternalServerError,
				"response":   "{}",
			},
			expectedCount: 0,
			expectErr:     true,
		},
	}

	for _, test := range tests {
		ts := startServer(t, "GET", "/_cat/indices?format=json", test.params)
		defer ts.Close()
		c := NewClient(ts.URL)

		indices, err := c.ListIndices()
		if test.expectErr {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
			idxs := *indices
			assert.Equal(t, test.expectedCount, len(idxs))
			for j, idx := range idxs {
				assert.Equal(t, fmt.Sprintf("idx%d", j), idx.Index)
			}
		}
	}
}

func TestCreateIndex(t *testing.T) {
	tests := []struct {
		params    map[string]interface{}
		expectErr bool
	}{
		{
			params: map[string]interface{}{
				"indexName":  "idx0",
				"statusCode": http.StatusOK,
				"response":   IndexCreatedResponse,
			},
			expectErr: false,
		},
		{
			params: map[string]interface{}{
				"indexName":  "idx1",
				"statusCode": http.StatusBadRequest,
				"response":   IndexAlreadyExistsResponse,
			},
			expectErr: true,
		},
	}

	for _, test := range tests {
		indexName := test.params["indexName"].(string)
		ts := startServer(t, "PUT", "/"+indexName, test.params)
		defer ts.Close()
		c := NewClient(ts.URL)

		rs, err := c.CreateIndex(indexName)
		if test.expectErr {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
			assert.True(t, rs.Acknowledged)
		}
	}
}

func TestIndexExists(t *testing.T) {
	tests := []struct {
		params map[string]interface{}
		exists bool
	}{
		{
			params: map[string]interface{}{
				"indexName":  "idx0",
				"statusCode": http.StatusOK,
			},
			exists: true,
		},
		{
			params: map[string]interface{}{
				"indexName":  "idx1",
				"statusCode": http.StatusNotFound,
			},
			exists: false,
		},
	}

	for _, test := range tests {
		indexName := test.params["indexName"].(string)
		ts := startServer(t, "HEAD", "/"+indexName, test.params)
		defer ts.Close()
		c := NewClient(ts.URL)

		exists, err := c.IndexExists(indexName)
		assert.NoError(t, err)
		assert.Equal(t, test.exists, exists)
	}
}

func TestDeleteIndex(t *testing.T) {
	tests := []struct {
		params         map[string]interface{}
		expectedStatus int
	}{
		{
			params: map[string]interface{}{
				"indexName":  "idx0",
				"statusCode": http.StatusOK,
				"response":   IndexDeletedResponse,
			},
			expectedStatus: 0,
		},
		{
			params: map[string]interface{}{
				"indexName":  "idx1",
				"statusCode": http.StatusNotFound,
				"response":   IndexNotFoundResponse,
			},
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, test := range tests {
		indexName := test.params["indexName"].(string)
		ts := startServer(t, "DELETE", "/"+indexName, test.params)
		defer ts.Close()
		c := NewClient(ts.URL)

		rs, err := c.DeleteIndex(indexName)
		assert.NoError(t, err)
		assert.Equal(t, test.expectedStatus, rs.Status)
	}
}

func TestIndexLogs(t *testing.T) {
	tests := []struct {
		params           map[string]interface{}
		indexRequest     string
		expectServerCall bool
	}{
		{
			params: map[string]interface{}{
				"indexName": "idx0",
			},
			indexRequest:     LaunchWithoutTestItems,
			expectServerCall: false,
		},
		{
			params: map[string]interface{}{
				"indexName": "idx1",
			},
			indexRequest:     LaunchWithTestItemsWithoutLogs,
			expectServerCall: false,
		},
		{
			params: map[string]interface{}{
				"indexName":  "idx2",
				"request":    LogIndexRequest,
				"response":   LogIndexResponse,
				"statusCode": http.StatusOK,
			},
			indexRequest:     LaunchWithTestItemsWithLogs,
			expectServerCall: true,
		},
	}

	for _, test := range tests {
		var esURL string
		indexName := test.params["indexName"].(string)
		if test.expectServerCall {
			ts := startServer(t, "PUT", "/_bulk", test.params)
			defer ts.Close()
			esURL = ts.URL
		}
		c := NewClient(esURL)

		launch := &Launch{}
		err := json.Unmarshal([]byte(test.indexRequest), launch)
		assert.NoError(t, err)

		_, err = c.IndexLogs(indexName, launch)
		assert.NoError(t, err)
	}
}

func startServer(t *testing.T, expectedMethod string, expectedURI string, params map[string]interface{}) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, expectedMethod, r.Method)
		assert.Equal(t, expectedURI, r.URL.RequestURI())
		expectedRq, ok := params["request"]
		if ok {
			defer r.Body.Close()
			rq, err := ioutil.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Equal(t, expectedRq, string(rq))
		}
		w.WriteHeader(params["statusCode"].(int))
		rs, ok := params["response"]
		if ok {
			w.Write([]byte(rs.(string)))
		}
	}))

	return ts
}
