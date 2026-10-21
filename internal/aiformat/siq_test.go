package aiformat

import (
	"bytes"
	"context"
	"reflect"
	"siplatform/internal/siq"
	"testing"
	"time"
)

func TestProvenanceSurvivesSIQ(t *testing.T) {
	d := Document(result(), "job1", "model", time.Now())
	var b bytes.Buffer
	if err := siq.Write(context.Background(), &b, d, nil); err != nil {
		t.Fatal(err)
	}
	archive, err := siq.Read(context.Background(), bytes.NewReader(b.Bytes()), int64(b.Len()), siq.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.Rounds[0].Themes[0].Questions[0].Info, archive.Document.Rounds[0].Themes[0].Questions[0].Info) {
		t.Fatal("provenance lost")
	}
}
