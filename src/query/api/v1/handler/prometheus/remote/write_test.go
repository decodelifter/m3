// Copyright (c) 2018 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package remote

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/m3db/m3/src/cmd/services/m3coordinator/ingest"
	"github.com/m3db/m3/src/dbnode/x/metrics"
	"github.com/m3db/m3/src/query/api/v1/handler/prometheus/remote/test"
	"github.com/m3db/m3/src/query/models"
	"github.com/m3db/m3/src/query/util/logging"
	xclock "github.com/m3db/m3/src/x/clock"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/tally"
)

func TestPromWriteParsing(t *testing.T) {
	logging.InitWithCores(nil)

	promWrite := &PromWriteHandler{}

	promReq := test.GeneratePromWriteRequest()
	promReqBody := test.GeneratePromWriteRequestBody(t, promReq)
	req, _ := http.NewRequest("POST", PromWriteURL, promReqBody)

	r, err := promWrite.parseRequest(req)
	require.Nil(t, err, "unable to parse request")
	require.Equal(t, len(r.Timeseries), 2)
}

func TestPromWrite(t *testing.T) {
	logging.InitWithCores(nil)

	ctrl := gomock.NewController(t)
	mockDownsamplerAndWriter := ingest.NewMockDownsamplerAndWriter(ctrl)
	mockDownsamplerAndWriter.EXPECT().WriteBatch(gomock.Any(), gomock.Any())

	promWrite := &PromWriteHandler{downsamplerAndWriter: mockDownsamplerAndWriter}

	promReq := test.GeneratePromWriteRequest()
	promReqBody := test.GeneratePromWriteRequestBody(t, promReq)
	req, _ := http.NewRequest("POST", PromWriteURL, promReqBody)

	r, err := promWrite.parseRequest(req)
	require.Nil(t, err, "unable to parse request")

	writeErr := promWrite.write(context.TODO(), r)
	require.NoError(t, writeErr)
}

func TestPromWriteError(t *testing.T) {
	logging.InitWithCores(nil)

	anError := fmt.Errorf("an error")

	ctrl := gomock.NewController(t)
	mockDownsamplerAndWriter := ingest.NewMockDownsamplerAndWriter(ctrl)
	mockDownsamplerAndWriter.EXPECT().
		WriteBatch(gomock.Any(), gomock.Any()).
		Return(anError)

	promWrite, err := NewPromWriteHandler(mockDownsamplerAndWriter,
		models.NewTagOptions(), tally.NoopScope)
	require.NoError(t, err)

	promReq := test.GeneratePromWriteRequest()
	promReqBody := test.GeneratePromWriteRequestBody(t, promReq)
	req, err := http.NewRequest("POST", PromWriteURL, promReqBody)
	require.NoError(t, err)

	writer := httptest.NewRecorder()
	promWrite.ServeHTTP(writer, req)
	resp := writer.Result()
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	body, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)
	require.True(t, bytes.Contains(body, []byte(anError.Error())))
}

func TestWriteErrorMetricCount(t *testing.T) {
	logging.InitWithCores(nil)

	ctrl := gomock.NewController(t)
	mockDownsamplerAndWriter := ingest.NewMockDownsamplerAndWriter(ctrl)
	mockDownsamplerAndWriter.EXPECT().WriteBatch(gomock.Any(), gomock.Any())

	reporter := xmetrics.NewTestStatsReporter(xmetrics.NewTestStatsReporterOptions())
	scope, closer := tally.NewRootScope(tally.ScopeOptions{Reporter: reporter}, time.Millisecond)
	defer closer.Close()
	writeMetrics := newPromWriteMetrics(scope)

	promWrite := &PromWriteHandler{
		downsamplerAndWriter: mockDownsamplerAndWriter,
		promWriteMetrics:     writeMetrics,
	}
	req, _ := http.NewRequest("POST", PromWriteURL, nil)
	promWrite.ServeHTTP(httptest.NewRecorder(), req)

	foundMetric := xclock.WaitUntil(func() bool {
		found := reporter.Counters()["write.errors"]
		return found == 1
	}, 5*time.Second)
	require.True(t, foundMetric)
}
