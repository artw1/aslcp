package demo

import (
	"io"
	"net/http"
	"strings"
	"time"
)

// radioIDCSV is a tiny placeholder user list in the shape of radioid.net's
// user.csv. Names and places are made up.
const radioIDCSV = `RADIO_ID,CALLSIGN,FIRST_NAME,LAST_NAME,CITY,STATE,COUNTRY
1234567,N0CALL,Alex,Example,Springfield,Illinois,United States
1234568,N0DEMO,Sam,Example,Anytown,Ohio,United States
1234569,N0TEST,Robin,Example,Shelbyville,,Canada
1234570,N0AAD,Pat,Example,Demoville,Texas,United States
1234571,N0AAE,Kim,Example,Demoville,Texas,United States
1234572,N0AAF,Lee,Example,Springfield,Illinois,United States
1234573,N0AAG,Jo,Example,Nowhere,Utah,United States
1234574,N0AAH,Max,Example,Metropolis,Illinois,United States
1234575,N0AAI,Sky,Example,Shelbyville,,Germany
1234576,N1CALL,Drew,Example,Springfield,Illinois,United States
1234577,N2CALL,Casey,Example,Metropolis,Illinois,United States
1234578,N3CALL,Jamie,Example,Anytown,Ohio,United States
`

type radioIDTransport struct{}

func (radioIDTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	h := http.Header{}
	h.Set("ETag", `"demo"`)
	return &http.Response{StatusCode: 200, Status: "200 OK", Header: h, Request: r, ProtoMajor: 1, ProtoMinor: 1,
		ContentLength: int64(len(radioIDCSV)), Body: io.NopCloser(strings.NewReader(radioIDCSV))}, nil
}

// RadioIDClient is an HTTP client that answers the radioid.net download with
// the placeholder list, so the demo needs no network.
func RadioIDClient() *http.Client {
	return &http.Client{Transport: radioIDTransport{}, Timeout: 10 * time.Second}
}
