# upload example

File uploads with craftgo - single files, **multiple files (`file[]`)**, and
multipart requests that mix many field shapes in one body. Every handler is
implemented against a small in-memory store and actually reads the uploaded
parts.

## Run

```bash
cd example/upload
go run .           # listens on :8080, docs at /docs
```

## Endpoints (`MediaService`, prefix `/api/media`)

| Method | Path | Shows |
|--------|------|-------|
| `POST` | `/users/{userId}/avatar` | single `file @form` + `@maxSize` + `@mimeTypes` + path param |
| `POST` | `/documents` | single file + required/optional form text |
| `POST` | `/notes/{noteId}/attachments` | single file, tight `@maxSize(500KB)` |
| `POST` | `/albums/{albumId}/gallery` | **`file[]` (1–20 photos)** + optional `cover` file + `string[]` tags + enum + bool + int + path |
| `GET` | `/{id}` | returns stored metadata, or `404 MediaNotFound` |
| `DELETE` | `/{id}` | idempotent delete |

## The multipart wire format

Arrays ride as **repeated parts with the same field name** - `photos` appears
once per file, `tags` once per value:

```bash
# single avatar
curl -X POST http://localhost:8080/api/media/users/u123/avatar \
  -F "image=@a.png;type=image/png"

# multi-file gallery: 3 photos + cover + many form fields
curl -X POST http://localhost:8080/api/media/albums/sum25/gallery \
  -F "photos=@a.png" -F "photos=@b.png" -F "photos=@c.png" \
  -F "cover=@hero.png" \
  -F "title=Summer 2026" -F "description=Beach trip" \
  -F "tags=summer" -F "tags=beach" \
  -F "visibility=public" -F "featured=true" -F "priority=10"
```

The gallery response echoes one entry per uploaded photo (id, filename, size,
mime) plus the cover and all the form fields. Sending 0 photos returns `400`
(the `@minItems(1)` on `photos file[]`).

## How a `file[]` binds

`photos file[]` lowers to `[]*multipart.FileHeader`, bound from
`r.MultipartForm.File["photos"]`; `@minItems` / `@maxItems` validate the count.
A single `file` (like `cover`) uses `r.FormFile`. In OpenAPI the array renders
as `{type: array, items: {type: string, format: binary}}`, so generated clients
type it as an array of files. See `internal/service/media-service/` for the
handlers and `svccontext/store.go` for the in-memory store.
