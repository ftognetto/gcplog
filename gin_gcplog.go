package gcplog

import (
	"bytes"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

type bodyLogWriter struct {
    gin.ResponseWriter
    body *bytes.Buffer
}

func (w bodyLogWriter) Write(b []byte) (int, error) {
    w.body.Write(b)
    return w.ResponseWriter.Write(b)
}

func Gin(gcplog *GcpLog) gin.HandlerFunc {

	return func(c *gin.Context) {

		// before request
		// log the body maybe..
		// ...do something
		blw := &bodyLogWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
    	c.Writer = blw

		defer func(begin time.Time) {

			// after request
			status := c.Writer.Status()
			log := c.Request.Method + " " + c.Request.URL.Path
			responseMeta := ResponseMetadata{
				Status:  c.Writer.Status(),
				Size:    c.Writer.Size(),
				Latency: time.Since(begin),
			}

			if status < 400 {
				gcplog.LogRM(log, c.Request, &responseMeta)
				return
			}

			var err error
			if len(c.Errors) > 0 {
				err = c.Errors.Last().Err
			} else {
				err = fmt.Errorf(blw.body.String())
			}

			if status >= 400 && status < 500 {
				gcplog.WarnRM(err, c.Request, &responseMeta)
			} else {
				gcplog.ErrorRM(err, c.Request, &responseMeta)
			}
		}(time.Now())

		
		c.Next()
	}
}
