package thtp

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/telehash/gogotelehash"
	"github.com/telehash/gogotelehash/e3x"
	"github.com/telehash/gogotelehash/internal/hashname"
)

var (
	_ http.RoundTripper = (*RoundTripper)(nil)
)

type RoundTripper struct {
	Endpoint *telehash.Endpoint
	Resolver Resolver
}

type Resolver interface {
	Resolve(hn hashname.H) (*e3x.Identity, error)
}

func NewClient(e *telehash.Endpoint) *http.Client {
	return &http.Client{Transport: &RoundTripper{Endpoint: e}}
}

// RegisterDefaultTransport registers the THTP protocol with http.DefaultTransport
// and binds it to the provided Endpoint.
func RegisterDefaultTransport(e *telehash.Endpoint) {
	t := http.DefaultTransport.(*http.Transport)
	t.RegisterProtocol("thtp", &RoundTripper{Endpoint: e})
}

func (rt *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var (
		hashname = hashname.H(req.URL.Host)
		c        *e3x.Channel
		resp     *http.Response
		err      error
	)

	x := rt.Endpoint.GetExchange(hashname)
	if x == nil {
		return nil, e3x.UnreachableEndpointError(hashname)
	}

	c, err = x.Open("thtp", true)
	if err != nil {
		c.Close()
		return nil, err
	}

	err = rt.writeRequest(req, c)
	if err != nil {
		c.Close()
		return nil, err
	}

	resp, err = rt.readResponse(c)
	if err != nil {
		c.Close()
		return nil, err
	}

	resp.Request = req
	return resp, nil
}

func (rt *RoundTripper) writeRequest(req *http.Request, c *e3x.Channel) error {
	var (
		w   = bufio.NewWriterSize(c, 1200)
		err error
	)

	{ // write header
		var (
			header     = make(map[string]string, len(req.Header)+2)
			headerData []byte
			headerLen  [2]byte
		)

		for k, v := range req.Header {
			if len(v) == 0 {
				continue
			}

			e := v[0]
			if e == "" {
				continue
			}

			header[k] = e
		}

		header[":method"] = strings.ToLower(req.Method)
		header[":path"] = req.URL.RequestURI()

		headerData, err = json.Marshal(header)
		if err != nil {
			return err
		}

		binary.BigEndian.PutUint16(headerLen[:], uint16(len(headerData)))

		_, err = w.Write(headerLen[:])
		if err != nil {
			return err
		}

		_, err = w.Write(headerData)
		if err != nil {
			return err
		}
	}

	if req.Body != nil { // write body
		_, err = io.Copy(w, req.Body)
		if err != nil {
			return err
		}

		err = w.Flush()
		if err != nil {
			return err
		}
	}

	return nil
}

func (rt *RoundTripper) readResponse(c *e3x.Channel) (*http.Response, error) {
	var (
		r    = newClientPacketReadCloser(c)
		resp *http.Response
		err  error
	)

	{ // read the header
		var (
			headLenData [2]byte
			headLen     uint16
			headerData  []byte
			header      map[string]interface{}
		)

		_, err = io.ReadFull(r, headLenData[:])
		if err != nil {
			if err == io.EOF {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}

		headLen = binary.BigEndian.Uint16(headLenData[:])
		headerData = make([]byte, headLen)
		_, err = io.ReadFull(r, headerData)
		if err != nil {
			if err == io.EOF {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}

		err = json.Unmarshal(headerData, &header)
		if err != nil {
			return nil, err
		}

		resp = &http.Response{}

		if v, p := header[":status"]; p && v != nil {
			var i int

			switch w := v.(type) {
			case int:
				i = w
			case int64:
				i = int(w)
			case float32:
				i = int(w)
			case float64:
				i = int(w)
			}

			if i > 0 {
				resp.StatusCode = i
				resp.Status = http.StatusText(i)
			}

			delete(header, ":status")
		}
		if resp.StatusCode == 0 {
			return nil, &http.ProtocolError{"missing `status` header"}
		}

		resp.Header = make(http.Header, len(header))
		resp.Proto = "1.1"
		resp.ProtoMajor = 1
		resp.ProtoMinor = 1

		for k, v := range header {
			if s, ok := v.(string); !ok || s == "" {
				if k != "status" {
					resp.Header.Set(http.CanonicalHeaderKey(k), s)
				}
			}
		}
	}

	{ // set the body
		resp.Body = r
	}

	return resp, nil
}

type clientPacketReadCloser struct {
	io.Reader
	io.Closer
}

func newClientPacketReadCloser(c *e3x.Channel) io.ReadCloser {
	return &clientPacketReadCloser{
		Reader: bufio.NewReaderSize(c, 64*1024),
		Closer: c,
	}
}
