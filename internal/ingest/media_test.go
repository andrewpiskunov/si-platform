package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"siplatform/internal/blob"
	"siplatform/internal/library"
	"siplatform/internal/media"
	"siplatform/internal/packs"
	"siplatform/internal/retention"
	"siplatform/internal/siq"
)

// Media purpose and kind must survive restart; missing isolation cannot silently produce a ready asset.
func TestMediaUploadPurposeLimitsAndUnavailableProcessor(t *testing.T) {
	f := setup(t)
	if _, err := f.service.CreateMedia(bg, f.owner, "file.png", 100<<20+1, "image"); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized media", err)
	}
	if _, err := f.service.CreateMedia(bg, f.owner, "file.png", 1, "html"); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown media kind", err)
	}
	u, err := f.service.CreateMedia(bg, f.owner, "file.png", 4, "image")
	if err != nil || u.Purpose != "media" || u.Asset != nil {
		t.Fatal(u, err)
	}
	foreign := actor(t, f.pool, "other", "session")
	if _, err = f.service.Get(bg, foreign, u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign upload", err)
	}
	if _, err = f.service.Append(bg, f.owner, u.ID, 0, strings.NewReader("data"), 4); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Complete(bg, f.owner, u.ID); err != nil {
		t.Fatal(err)
	}
	f.service.Close()
	next, err := New(f.pool, f.objects, f.lib, Options{Directory: f.dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	f.service = next
	next.Start(bg)
	u = awaitStatus(t, f, u.ID, "failed")
	if u.Purpose != "media" || u.Error != "media_pending" || u.Asset != nil {
		t.Fatal(u)
	}
}

// Names rejected by the archive asset profile must fail before upload and preparation consume resources.
func TestMediaUploadRejectsNamesUnusableForAttachment(t *testing.T) {
	for _, name := range []string{"photo%.png", "photo:one.png", "photo.zip"} {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			if _, err := f.service.CreateMedia(bg, f.owner, name, 4, "image"); !errors.Is(err, ErrInvalid) {
				t.Fatal("unattachable name accepted", err)
			}
		})
	}
}

func pngMedia(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 32, 24))
	for y := range 24 {
		for x := range 32 {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 7), uint8(y * 9), 80, 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func realProcessor(t *testing.T) *media.Processor {
	t.Helper()
	if os.Getenv("MEDIA_INTEGRATION") != "1" {
		t.Skip("set MEDIA_INTEGRATION=1 for real sandbox media tests")
	}
	p, err := media.New(media.Options{FFmpeg: os.Getenv("MEDIA_FFMPEG"), FFprobe: os.Getenv("MEDIA_FFPROBE"), Sandbox: "/usr/bin/bwrap", Prlimit: "/usr/bin/prlimit", Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func mediaArchive(t *testing.T, data []byte) []byte {
	t.Helper()
	xml := strings.Replace(textXML, `<item type="text">Question?</item>`, `<item type="image" isRef="true">photo.png</item>`, 1)
	return archive(t, xml, map[string]string{"Images/photo.png": string(data)})
}

// Without a configured isolated processor, even recognizable images require an explicit pending diagnosis.
func TestSIQMissingProcessorKeepsOriginalAndMediaPending(t *testing.T) {
	f := setup(t)
	u := queue(t, f, mediaArchive(t, pngMedia(t)))
	f.service.Start(bg)
	u = awaitStatus(t, f, u.ID, "ready")
	d, err := f.lib.Get(bg, f.owner, u.DraftID, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range d.AssetIssues {
		if issue.Code == "media_pending" && issue.Path == "Images/photo.png" {
			found = true
		}
	}
	if !found || len(d.Assets) != 1 || d.Assets[0].Prepared != nil {
		t.Fatal("missing pending image diagnosis", d.AssetIssues, d.Assets)
	}
}

// Real preparation must return a private result, preserve the original and reserve both variants before publication.
func TestRealMediaUploadAndSIQPreparation(t *testing.T) {
	processor := realProcessor(t)
	for _, purpose := range []string{"media", "siq"} {
		t.Run(purpose, func(t *testing.T) {
			f := setup(t)
			f.service.media = processor
			original := pngMedia(t)
			data := original
			var u Upload
			var err error
			if purpose == "media" {
				u, err = f.service.CreateMedia(bg, f.owner, "photo.png", int64(len(data)), "image")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.service.Append(bg, f.owner, u.ID, 0, bytes.NewReader(data), int64(len(data))); err != nil {
					t.Fatal(err)
				}
				u, err = f.service.Complete(bg, f.owner, u.ID)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				data = mediaArchive(t, original)
				u = queue(t, f, data)
			}
			f.service.Start(bg)
			u = awaitStatus(t, f, u.ID, "ready")
			var asset library.Asset
			if purpose == "media" {
				if u.Asset == nil || u.DraftID != "" {
					t.Fatal("missing standalone result", u)
				}
				asset = *u.Asset
				d, err := f.lib.Create(bg, f.owner, library.Save{Document: archiveDocument(t)})
				if err != nil {
					t.Fatal(err)
				}
				attached, err := f.lib.AttachAsset(bg, f.owner, d.Card.ID, 1, u.ID, asset)
				if err != nil || len(attached.Assets) != 1 {
					t.Fatal(attached, err)
				}
			} else {
				d, err := f.lib.Get(bg, f.owner, u.DraftID, 0)
				if err != nil || len(d.Assets) != 1 || !packs.Playable(d.Issues) || !packs.Playable(d.PlayIssues) {
					t.Fatal(d.Issues, d.PlayIssues, err)
				}
				asset = d.Assets[0]
				r, err := f.objects.Get(bg, d.OriginalKey, 0, -1)
				if err != nil {
					t.Fatal(err)
				}
				b, err := io.ReadAll(r)
				r.Close()
				if err != nil || !bytes.Equal(b, data) {
					t.Fatal("SIQ original changed", err)
				}
				var exported bytes.Buffer
				err = siq.Write(bg, &exported, d.Document, []siq.Asset{{Name: asset.Name, Key: "images/photo.png", Size: asset.Size, SHA256: asset.Key, Open: func() (io.ReadCloser, error) { return f.objects.Get(bg, asset.Key, 0, -1) }}})
				if err != nil {
					t.Fatal(err)
				}
				restored, err := siq.Read(bg, bytes.NewReader(exported.Bytes()), int64(exported.Len()), siq.DefaultLimits())
				if err != nil || len(restored.Assets) != 1 {
					t.Fatal(err)
				}
				r, err = restored.Assets[0].Open()
				if err != nil {
					t.Fatal(err)
				}
				b, err = io.ReadAll(r)
				r.Close()
				if err != nil || !bytes.Equal(b, original) {
					t.Fatal("export replaced original with derivative", err)
				}
			}
			sum := sha256.Sum256(original)
			if asset.Key != hex.EncodeToString(sum[:]) || asset.Size != int64(len(original)) || asset.Prepared == nil {
				t.Fatal(asset)
			}
			var reserved []string
			if err = f.pool.QueryRow(bg, "SELECT stored_keys FROM uploads WHERE id=$1", u.ID).Scan(&reserved); err != nil {
				t.Fatal(err)
			}
			for _, variant := range []media.Variant{asset.Prepared.Standard, asset.Prepared.Economy} {
				found := false
				for _, key := range reserved {
					if key == variant.Key {
						found = true
					}
				}
				if !found {
					t.Fatal("unreserved derivative", variant.Key, reserved)
				}
				meta, err := f.objects.Stat(bg, variant.Key)
				if err != nil || meta.Size != variant.Size || meta.ContentType != variant.MIME {
					t.Fatal(meta, err)
				}
			}
		})
	}
}

// The final ready transition must recheck credentials and fencing even after expensive preparation.
func TestPreparedMediaCompletionRechecksAuthorizationAndEpoch(t *testing.T) {
	for _, mode := range []string{"revoked", "epoch", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			u, err := f.service.CreateMedia(bg, f.owner, "photo.png", 4, "image")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.service.Append(bg, f.owner, u.ID, 0, strings.NewReader("data"), 4); err != nil {
				t.Fatal(err)
			}
			if _, err = f.service.Complete(bg, f.owner, u.ID); err != nil {
				t.Fatal(err)
			}
			j, err := f.service.claim(bg, 0)
			if err != nil {
				t.Fatal(err)
			}
			asset := library.Asset{Name: "Images/photo.png", Key: strings.Repeat("1", 64), Size: 4, ContentType: "image/png", Prepared: &media.Prepared{Kind: "image", Width: 1, Height: 1, Standard: media.Variant{Key: strings.Repeat("2", 64), MIME: "image/png", Size: 1}, Economy: media.Variant{Key: strings.Repeat("3", 64), MIME: "image/png", Size: 1}}}
			finishCtx := bg
			switch mode {
			case "revoked":
				_, err = f.pool.Exec(bg, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", f.owner.SessionID)
			case "epoch":
				_, err = f.pool.Exec(bg, "UPDATE ingest_owner SET epoch=1")
			case "canceled":
				c, cancel := context.WithCancel(bg)
				cancel()
				finishCtx = c
			}
			if err != nil {
				t.Fatal(err)
			}
			f.service.finish(finishCtx, j, 0, "", &asset, "")
			var status, code string
			var result []byte
			if err = f.pool.QueryRow(bg, "SELECT status,error,result_asset FROM uploads WHERE id=$1", u.ID).Scan(&status, &code, &result); err != nil {
				t.Fatal(err)
			}
			if mode == "revoked" {
				if status != "failed" || code != "authorization_expired" {
					t.Fatal(status, code)
				}
			} else if status != "processing" {
				t.Fatal("old/canceled worker changed job", status)
			}
			if len(result) != 0 {
				t.Fatal("unauthorized result published", string(result))
			}
		})
	}
}

// Interrupting publication after preparation must preserve reservations and staged bytes for restart.
func TestRealMediaCanceledPublicationRecoversWithReservations(t *testing.T) {
	processor := realProcessor(t)
	f := setup(t)
	f.service.media = processor
	hold := &mediaPutGate{Store: f.objects, entered: make(chan string, 1)}
	f.service.objects = hold
	data := pngMedia(t)
	u, err := f.service.CreateMedia(bg, f.owner, "photo.png", int64(len(data)), "image")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Append(bg, f.owner, u.ID, 0, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Complete(bg, f.owner, u.ID); err != nil {
		t.Fatal(err)
	}
	f.service.Start(bg)
	var publishingKey string
	select {
	case publishingKey = <-hold.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("prepared object never reached Put")
	}
	var reserved bool
	if err = f.pool.QueryRow(bg, "SELECT stored_keys @> ARRAY[$2]::text[] FROM uploads WHERE id=$1", u.ID, publishingKey).Scan(&reserved); err != nil || !reserved {
		t.Fatal("Put preceded durable reservation", reserved, err)
	}
	f.service.Close()
	var status string
	if err = f.pool.QueryRow(bg, "SELECT status FROM uploads WHERE id=$1", u.ID).Scan(&status); err != nil || status != "processing" {
		t.Fatal("canceled job finalized", status, err)
	}
	next, err := New(f.pool, f.objects, f.lib, Options{Directory: f.dir, Media: processor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	f.service = next
	next.Start(bg)
	u = awaitStatus(t, f, u.ID, "ready")
	if u.Asset == nil || u.Asset.Prepared == nil {
		t.Fatal(u)
	}
	var stillReserved bool
	if err = f.pool.QueryRow(bg, "SELECT stored_keys @> ARRAY[$2]::text[] FROM uploads WHERE id=$1", u.ID, publishingKey).Scan(&stillReserved); err != nil || !stillReserved {
		t.Fatal("restart lost reservation", stillReserved, err)
	}
}

type mediaPutGate struct {
	blob.Store
	entered chan string
}

func (s *mediaPutGate) Put(ctx context.Context, key string, r io.Reader, size int64, ct string) error {
	select {
	case s.entered <- key:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}

// Revocation during real object writes must prevent ready publication and leave collectible reservations.
func TestRealMediaRevokedDuringPublicationFailsAndCollectsObjects(t *testing.T) {
	processor := realProcessor(t)
	f := setup(t)
	f.service.media = processor
	session := f.owner
	write := actor(t, f.pool, "owner", "api", "drafts:write")
	f.owner = write
	data := pngMedia(t)
	u, err := f.service.CreateMedia(bg, write, "photo.png", int64(len(data)), "image")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Append(bg, write, u.ID, 0, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Complete(bg, write, u.ID); err != nil {
		t.Fatal(err)
	}
	f.owner = session
	f.service.objects = &revokingStore{Store: f.objects, pool: f.pool, credential: write.SessionID}
	f.service.Start(bg)
	u = awaitStatus(t, f, u.ID, "failed")
	if u.Error != "authorization_expired" || u.Asset != nil {
		t.Fatal(u)
	}
	f.service.Close()
	next, err := New(f.pool, f.objects, f.lib, Options{Directory: f.dir, Media: processor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	var keys []string
	if err = f.pool.QueryRow(bg, "SELECT stored_keys FROM uploads WHERE id=$1", u.ID).Scan(&keys); err != nil || len(keys) < 2 {
		t.Fatal("lost source/derivative ledger", keys, err)
	}
	for _, key := range keys {
		if _, err = f.objects.Stat(bg, key); err != nil {
			t.Fatal("fixture did not store object", err)
		}
	}
	if _, err = f.pool.Exec(bg, "UPDATE uploads SET updated_at=now()-interval '2 hours' WHERE id=$1", u.ID); err != nil {
		t.Fatal(err)
	}
	var epoch int64
	if err = f.pool.QueryRow(bg, "SELECT epoch FROM ingest_owner").Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	next.cleanup(bg, epoch)
	if _, err = retention.New(f.pool, f.objects).Sweep(bg); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if _, err = f.objects.Stat(bg, key); !errors.Is(err, blob.ErrNotFound) {
			t.Fatal("failed media object leaked", key, err)
		}
	}
}

// A damaged media entry produces a filename-specific issue while the SIQ and original media stay recoverable.
func TestRealInvalidSIQMediaRetainsEditableOriginal(t *testing.T) {
	processor := realProcessor(t)
	f := setup(t)
	f.service.media = processor
	source := []byte("\x89PNG\r\n\x1a\nbroken")
	data := mediaArchive(t, source)
	u := queue(t, f, data)
	f.service.Start(bg)
	u = awaitStatus(t, f, u.ID, "ready")
	d, err := f.lib.Get(bg, f.owner, u.DraftID, 0)
	if err != nil || len(d.Assets) != 1 || d.Assets[0].Prepared != nil {
		t.Fatal(d, err)
	}
	found := false
	for _, issue := range d.AssetIssues {
		if issue.Path == "Images/photo.png" && strings.HasPrefix(issue.Code, "media_") {
			found = true
		}
	}
	if !found || packs.Playable(d.Issues) {
		t.Fatal("lost media error", d.Issues)
	}
	for key, want := range map[string][]byte{d.OriginalKey: data, d.Assets[0].Key: source} {
		r, err := f.objects.Get(bg, key, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		r.Close()
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("original changed", err)
		}
	}
}

// MP4 is a shared container; verified audio originals must retain an audio MIME on a second upload.
func TestRealPreparedAudioCanBeUploadedAgain(t *testing.T) {
	processor := realProcessor(t)
	f := setup(t)
	f.service.media = processor
	f.service.Start(bg)
	data := make([]byte, 44+16000)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 8000)
	binary.LittleEndian.PutUint32(data[28:], 16000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], 16000)
	for attempt := range 2 {
		u, err := f.service.CreateMedia(bg, f.owner, "sound.m4a", int64(len(data)), "audio")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.service.Append(bg, f.owner, u.ID, 0, bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatal(err)
		}
		if _, err = f.service.Complete(bg, f.owner, u.ID); err != nil {
			t.Fatal(err)
		}
		u = awaitStatus(t, f, u.ID, "ready")
		if u.Asset == nil || u.Asset.Prepared == nil || u.Asset.Prepared.Kind != "audio" {
			t.Fatal(u)
		}
		if attempt == 1 && u.Asset.ContentType != "audio/mp4" {
			t.Fatal("audio original classified as video", u.Asset.ContentType)
		}
		d, err := f.lib.Create(bg, f.owner, library.Save{Document: archiveDocument(t)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.lib.AttachAsset(bg, f.owner, d.Card.ID, 1, u.ID, *u.Asset); err != nil {
			t.Fatal("verified media cannot attach", err)
		}
		r, err := f.objects.Get(bg, u.Asset.Prepared.Standard.Key, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		data, err = io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Ready standalone bytes consume quota before attachment; their charge transfers
// once, while attachment to additional drafts pays for each immutable revision.
func TestMediaQuotaChargesReadyUploadsAndTransfersOnlyOnce(t *testing.T) {
	f := setup(t)
	doc := archiveDocument(t)
	first, err := f.lib.Create(bg, f.owner, library.Save{Document: doc})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.lib.Create(bg, f.owner, library.Save{Document: doc})
	if err != nil {
		t.Fatal(err)
	}
	const limit int64 = 10 << 30
	setFirstSize := func(size int64) {
		t.Helper()
		if _, err := f.pool.Exec(bg, "UPDATE library_revisions SET size_bytes=$2 WHERE pack_id=$1 AND revision=1", first.Card.ID, size); err != nil {
			t.Fatal(err)
		}
	}
	setFirstSize(limit - second.Card.SizeBytes - 100)
	asset := library.Asset{Name: "Images/photo.png", Key: strings.Repeat("1", 64), Size: 10, ContentType: "image/png", Prepared: &media.Prepared{Kind: "image", Width: 1, Height: 1, Standard: media.Variant{Key: strings.Repeat("2", 64), MIME: "image/png", Size: 20}, Economy: media.Variant{Key: strings.Repeat("3", 64), MIME: "image/png", Size: 30}}}
	complete := func() Upload {
		t.Helper()
		u, err := f.service.CreateMedia(bg, f.owner, "photo.png", 10, "image")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.service.Append(bg, f.owner, u.ID, 0, strings.NewReader("0123456789"), 10); err != nil {
			t.Fatal(err)
		}
		if _, err = f.service.Complete(bg, f.owner, u.ID); err != nil {
			t.Fatal(err)
		}
		j, err := f.service.claim(bg, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.service.finish(bg, j, 0, "", &asset, "")
		u, err = f.service.Get(bg, f.owner, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	ready := complete()
	if ready.Status != "ready" {
		t.Fatal(ready)
	}
	rejected := complete()
	if rejected.Status != "failed" || rejected.Error != "draft_quota" || rejected.Asset != nil {
		t.Fatal("unattached bytes escaped quota", rejected)
	}
	docJSON, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	revisionBytes := int64(len(docJSON)) + 60
	seedSize := limit - second.Card.SizeBytes - revisionBytes
	setFirstSize(seedSize)
	attached, err := f.lib.AttachAsset(bg, f.owner, first.Card.ID, 1, ready.ID, asset)
	if err != nil || attached.Card.Revision != 2 {
		t.Fatal("quota did not transfer", attached, err)
	}
	repeated, err := f.lib.AttachAsset(bg, f.owner, first.Card.ID, 1, ready.ID, asset)
	if err != nil || repeated.Card.Revision != 2 {
		t.Fatal("retry charged twice", repeated, err)
	}
	setFirstSize(seedSize - int64(len(docJSON)))
	if _, err = f.lib.AttachAsset(bg, f.owner, second.Card.ID, 1, ready.ID, asset); !errors.Is(err, library.ErrQuota) {
		t.Fatal("attachment to second draft released charge twice", err)
	}
	setFirstSize(seedSize - revisionBytes)
	if _, err = f.lib.AttachAsset(bg, f.owner, second.Card.ID, 1, ready.ID, asset); err != nil {
		t.Fatal("valid second attachment failed", err)
	}
}

// Nested prepared references must protect shared hashes even if no ready-upload reservation remains.
func TestCleanupPreservesDerivativeReferencedByLibraryRevision(t *testing.T) {
	f := setup(t)
	payload := []byte("verified derivative fixture")
	sum := sha256.Sum256(payload)
	key := hex.EncodeToString(sum[:])
	if err := f.objects.Put(bg, key, bytes.NewReader(payload), int64(len(payload)), "image/png"); err != nil {
		t.Fatal(err)
	}
	asset := library.Asset{Name: "Images/photo.png", Key: strings.Repeat("1", 64), Size: 1, ContentType: "image/png", Prepared: &media.Prepared{Kind: "image", Width: 10, Height: 10, Standard: media.Variant{Key: key, MIME: "image/png", Size: int64(len(payload))}, Economy: media.Variant{Key: key, MIME: "image/png", Size: int64(len(payload))}}}
	if _, err := f.lib.Create(bg, f.owner, library.Save{Document: archiveDocument(t), Assets: []library.Asset{asset}}); err != nil {
		t.Fatal(err)
	}
	u, err := f.service.Create(bg, f.owner, "failed.siq", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(bg, "UPDATE uploads SET status='failed',stored_keys=$2,updated_at=now()-interval '2 hours' WHERE id=$1", u.ID, []string{key}); err != nil {
		t.Fatal(err)
	}
	f.service.cleanup(bg, 0)
	if _, err = f.objects.Stat(bg, key); err != nil {
		t.Fatal("shared derivative deleted", err)
	}
}

func archiveDocument(t *testing.T) packs.Document {
	t.Helper()
	data := archive(t, textXML, nil)
	read, err := siq.Read(bg, bytes.NewReader(data), int64(len(data)), siq.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return read.Document
}
