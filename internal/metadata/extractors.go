package metadata

import (
	"encoding/binary"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// quickTimeEpoch is the base time used by ISO-BMFF/QuickTime timestamps.
var quickTimeEpoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)

// mediaMatch reports whether a sample is an image or video container that the
// media extractor knows how to inspect for embedded dates and fields.
func mediaMatch(sample Sample) bool {
	format, _, category := formatFromMagic(sample.Head)
	if format == "" {
		format, _, category = formatFromExtension(sample.Extension)
	}
	return category == "Image" || category == "Video"
}

// extractMedia pulls embedded date and field metadata from images and videos.
func extractMedia(sample Sample) (Result, error) {
	format, _, _ := formatFromMagic(sample.Head)
	if format == "" {
		format, _, _ = formatFromExtension(sample.Extension)
	}

	res := Result{}
	var date time.Time
	var dateSource string

	switch format {
	case "JPEG":
		if tiff := tiffBytesFromJPEG(sample.Head); tiff != nil {
			date, dateSource = exifCreateDate(tiff)
			collectEXIFFields(tiff, &res)
		}
	case "TIFF":
		if tiff := tiffBytesFromTIFF(sample.Head); tiff != nil {
			date, dateSource = exifCreateDate(tiff)
			collectEXIFFields(tiff, &res)
		}
	case "PNG":
		if tiff := pngEXIFFromSample(sample.Head); tiff != nil {
			date, dateSource = exifCreateDate(tiff)
		}
		collectPNGText(sample.Head, &res)
	case "WebP":
		if tiff := webpEXIFFromSample(sample.Head); tiff != nil {
			date, dateSource = exifCreateDate(tiff)
		}
	case "QuickTime", "MPEG", "AVI":
		if created, modified, ok := quickTimeDates(sample.Head); ok {
			if !created.Equal(quickTimeEpoch) {
				date, dateSource = created, "quicktime"
			} else if !modified.Equal(quickTimeEpoch) {
				date, dateSource = modified, "quicktime"
			}
		}
	}

	if !date.IsZero() {
		setDate(&res, date, dateSource)
	}
	return res, nil
}

// pdfMatch reports whether a sample is a PDF document.
func pdfMatch(sample Sample) bool {
	return detectFormat(sample).Format == "PDF"
}

// extractPDF reads title, author, subject, creator, producer and document
// dates from a PDF's Info dictionary.
func extractPDF(sample Sample) (Result, error) {
	res := Result{
		Format:   "PDF",
		MIMEType: "application/pdf",
		Category: "Document",
	}
	data := sample.Head

	if infoObj := pdfInfoObject(data); infoObj >= 0 {
		if dict := pdfObjectBytes(data, infoObj); len(dict) > 0 {
			pdfParseInfo(dict, &res)
		}
	}
	if res.Fields == nil {
		pdfParseInfo(data, &res)
	}
	if d := res.Fields["creation_date"]; d != "" {
		if t, ok := parsePDFDate(d); ok {
			setDate(&res, t, "pdf")
		}
	}
	return res, nil
}

// archiveMatch reports whether a sample is an archive/container format.
func archiveMatch(sample Sample) bool {
	return detectFormat(sample).Category == "Archive"
}

// extractArchive detects the concrete archive flavour (ZIP, OOXML, ODF, EPUB,
// 7z, RAR, TAR, GZIP, ISO) and, for ZIP-based documents, reads the core
// properties embedded in the bounded sample.
func extractArchive(sample Sample) (Result, error) {
	res := detectFormat(sample)

	format, mime, category := formatFromMagic(sample.Head)
	if format == "" {
		format, mime, category = formatFromExtension(sample.Extension)
	}
	res.Format = format
	res.MIMEType = mime
	res.Category = category

	if isZIP(sample.Head) {
		if name, ok := zipDocumentKind(sample); ok {
			res.Format = name
		}
		switch res.Format {
		case "EPUB", "ODF", "OOXML":
			for k, v := range zipCoreProps(sample) {
				addField(&res, k, v)
			}
			if d := res.Fields["created"]; d != "" {
				if t, ok := parseISO8601(d); ok {
					setDate(&res, t, res.Format)
				}
			}
		}
	}

	return res, nil
}

// audioMatch reports whether a sample is an audio container.
func audioMatch(sample Sample) bool {
	return detectFormat(sample).Category == "Audio"
}

// extractAudio reads embedded tags (ID3, Vorbis, RIFF INFO) from audio files.
func extractAudio(sample Sample) (Result, error) {
	format, mime, category := formatFromMagic(sample.Head)
	if format == "" {
		format, mime, category = formatFromExtension(sample.Extension)
	}
	res := Result{Format: format, MIMEType: mime, Category: category}

	switch {
	case strings.HasPrefix(string(sample.Head), "ID3"):
		parseID3v2(sample.Head, &res)
	case len(sample.Tail) >= 128 && string(sample.Tail[len(sample.Tail)-128:len(sample.Tail)-125]) == "TAG":
		parseID3v1(sample.Tail[len(sample.Tail)-128:], &res)
	}
	switch format {
	case "FLAC", "OGG":
		parseVorbisComments(sample.Head, &res)
	case "WAV":
		parseRIFFInfo(sample.Head, &res)
	}
	if d := res.Fields["creation_date"]; d != "" {
		if t, ok := parseAudioDate(d); ok {
			setDate(&res, t, format)
		}
	}
	return res, nil
}

// --------------------------- JPEG / EXIF helpers ---------------------------

// tiffBytesFromJPEG locates the APP1 "Exif\x00\x00" segment inside a JPEG.
func tiffBytesFromJPEG(data []byte) []byte {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return nil
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xff {
			i++
			continue
		}
		marker := data[i+1]
		if marker == 0xda { // start of scan
			break
		}
		if marker == 0xd8 || marker == 0xd9 || (marker >= 0xd0 && marker <= 0xd7) {
			i += 2
			continue
		}
		if i+4 > len(data) {
			break
		}
		segLen := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(data) {
			break
		}
		payload := data[i+4 : i+2+segLen]
		if marker == 0xe1 && len(payload) >= 6 && string(payload[:6]) == "Exif\x00\x00" {
			return payload[6:]
		}
		i += 2 + segLen
	}
	return nil
}

// tiffBytesFromTIFF returns the TIFF data as-is when it is a standalone TIFF.
func tiffBytesFromTIFF(data []byte) []byte {
	if isTIFF(data) {
		return data
	}
	return nil
}

// exifCreateDate scans IFD0 and the Exif sub-IFD for the preferred creation
// date (DateTimeOriginal, then CreateDate, then legacy DateTime).
func exifCreateDate(tiff []byte) (time.Time, string) {
	order, ifdOff, ok := tiffHeader(tiff)
	if !ok {
		return time.Time{}, ""
	}
	fallback := ""
	if s, ok := readTIFFString(tiff, order, ifdOff, 0x0132); ok && s != "" {
		fallback = s
	}
	if sub, ok := readTIFFLong(tiff, order, ifdOff, 0x8769); ok {
		if s, ok := readTIFFString(tiff, order, sub, 0x9003); ok {
			if t, ok := parseExifDate(s); ok {
				return t, "exif"
			}
		}
		if s, ok := readTIFFString(tiff, order, sub, 0x9004); ok {
			if t, ok := parseExifDate(s); ok {
				return t, "exif"
			}
		}
	}
	if s, ok := readTIFFString(tiff, order, ifdOff, 0x9003); ok {
		if t, ok := parseExifDate(s); ok {
			return t, "exif"
		}
	}
	if fallback != "" {
		if t, ok := parseExifDate(fallback); ok {
			return t, "exif"
		}
	}
	return time.Time{}, ""
}

// collectEXIFFields adds useful EXIF/TIFF fields (dimensions, make, model,
// software, ISO, focal length) to the result.
func collectEXIFFields(tiff []byte, res *Result) {
	order, ifdOff, ok := tiffHeader(tiff)
	if !ok {
		return
	}
	if w, h, ok := readTIFFDimensions(tiff, order, ifdOff); ok {
		addField(res, "image_width", strconv.Itoa(w))
		addField(res, "image_height", strconv.Itoa(h))
	}
	for _, m := range []struct {
		off   int
		tag   uint16
		field string
	}{{ifdOff, 0x010f, "make"}, {ifdOff, 0x0110, "model"}, {ifdOff, 0x0131, "software"}} {
		if s, ok := readTIFFString(tiff, order, m.off, m.tag); ok && s != "" {
			addField(res, m.field, s)
		}
	}
	if sub, ok := readTIFFLong(tiff, order, ifdOff, 0x8769); ok {
		for _, m := range []struct {
			off   int
			tag   uint16
			field string
		}{{sub, 0x8827, "iso_speed"}, {sub, 0x920a, "focal_length"}} {
			if v, ok := readTIFFLong(tiff, order, m.off, m.tag); ok && v != 0 {
				addField(res, m.field, strconv.Itoa(v))
			}
		}
	}
}

func tiffHeader(tiff []byte) (binary.ByteOrder, int, bool) {
	if len(tiff) < 8 {
		return nil, 0, false
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return nil, 0, false
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return nil, 0, false
	}
	return order, int(order.Uint32(tiff[4:8])), true
}

func readTIFFString(tiff []byte, order binary.ByteOrder, ifdOff int, tag uint16) (string, bool) {
	if ifdOff < 0 || ifdOff+2 > len(tiff) {
		return "", false
	}
	count := int(order.Uint16(tiff[ifdOff : ifdOff+2]))
	p := ifdOff + 2
	for i := 0; i < count; i++ {
		if p+12 > len(tiff) {
			break
		}
		entry := tiff[p : p+12]
		etag := order.Uint16(entry[0:2])
		etype := order.Uint16(entry[2:4])
		ecount := order.Uint32(entry[4:8])
		val := order.Uint32(entry[8:12])
		p += 12
		if etag != tag || int(ecount) < 1 {
			continue
		}
		if etype == 2 { // ASCII
			byteCount := int(ecount)
			if byteCount > len(tiff) {
				continue
			}
			var value []byte
			if byteCount <= 4 {
				end := 8 + byteCount
				if end > 12 {
					end = 12
				}
				value = entry[8:end]
			} else {
				off := int(val)
				if off < 0 || off+byteCount > len(tiff) {
					return "", false
				}
				value = tiff[off : off+byteCount]
			}
			s := strings.TrimSpace(strings.TrimRight(string(value), "\x00 "))
			return s, s != ""
		}
		return "", false
	}
	return "", false
}

func readTIFFLong(tiff []byte, order binary.ByteOrder, ifdOff int, tag uint16) (int, bool) {
	if ifdOff < 0 || ifdOff+2 > len(tiff) {
		return 0, false
	}
	count := int(order.Uint16(tiff[ifdOff : ifdOff+2]))
	p := ifdOff + 2
	for i := 0; i < count; i++ {
		if p+12 > len(tiff) {
			break
		}
		entry := tiff[p : p+12]
		etag := order.Uint16(entry[0:2])
		etype := order.Uint16(entry[2:4])
		val := order.Uint32(entry[8:12])
		p += 12
		if etag == tag {
			switch etype {
			case 3: // SHORT
				return int(val & 0xffff), true
			case 4, 7: // LONG or UNDEFINED pointer
				return int(val), true
			}
			return 0, false
		}
	}
	return 0, false
}

func readTIFFDimensions(tiff []byte, order binary.ByteOrder, ifdOff int) (int, int, bool) {
	w, wok := readTIFFLong(tiff, order, ifdOff, 0x0100)
	h, hok := readTIFFLong(tiff, order, ifdOff, 0x0101)
	if wok && hok && w > 0 && h > 0 {
		return w, h, true
	}
	return 0, 0, false
}

func parseExifDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(strings.Trim(s, "\x00"))
	for _, layout := range []string{"2006:01:02 15:04:05", "2006:01:02 15:04", "2006:01:02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ------------------------------ PNG helpers --------------------------------

func pngEXIFFromSample(data []byte) []byte {
	if len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		return nil
	}
	offset := 8
	for offset+8 <= len(data) {
		length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		kind := string(data[offset+4 : offset+8])
		if kind == "IEND" {
			break
		}
		if offset+8+length > len(data) {
			break
		}
		if kind == "eXIf" {
			return data[offset+8 : offset+8+length]
		}
		offset += 8 + length + 4
	}
	return nil
}

func collectPNGText(data []byte, res *Result) {
	if len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		return
	}
	offset := 8
	for offset+8 <= len(data) {
		length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		kind := string(data[offset+4 : offset+8])
		if kind == "IEND" {
			break
		}
		if offset+8+length > len(data) {
			break
		}
		chunk := data[offset+8 : offset+8+length]
		switch kind {
		case "tEXt":
			if i := indexOf(chunk, []byte{0}); i > 0 && i+1 < len(chunk) {
				key := strings.ToLower(string(chunk[:i]))
				addField(res, pngTextField(key), string(chunk[i+1:]))
			}
		case "iTXt":
			if i := indexOf(chunk, []byte{0}); i > 0 && i+1 < len(chunk) {
				key := strings.ToLower(string(chunk[:i]))
				rest := chunk[i+1:]
				// iTXt: keyword\0 compression\0 language\0 translated\0 text
				for skip := 0; skip < 3; skip++ {
					j := indexOf(rest, []byte{0})
					if j < 0 || j+1 > len(rest) {
						rest = nil
						break
					}
					rest = rest[j+1:]
				}
				if len(rest) > 0 {
					addField(res, pngTextField(key), string(rest))
				}
			}
		}
		offset += 8 + length + 4
	}
}

func pngTextField(key string) string {
	switch key {
	case "title", "artist", "author", "software", "comment", "description":
		return key
	case "creation time", "creation_time":
		return "creation_date"
	}
	return key
}

// ------------------------------ WebP helpers -------------------------------

func webpEXIFFromSample(data []byte) []byte {
	if len(data) < 20 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil
	}
	offset := 12
	for offset+8 <= len(data) {
		kind := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if offset+8+size > len(data) {
			break
		}
		if kind == "EXIF" {
			return data[offset+8 : offset+8+size]
		}
		offset += 8 + size + (size & 1)
	}
	return nil
}

// --------------------------- QuickTime / ISO-BMFF --------------------------

func quickTimeDates(data []byte) (time.Time, time.Time, bool) {
	payload, ok := boxPayload(data, "mvhd")
	if !ok || len(payload) < 12 {
		return time.Time{}, time.Time{}, false
	}
	var created, modified time.Time
	switch payload[0] {
	case 1:
		if len(payload) < 20 {
			return time.Time{}, time.Time{}, false
		}
		created = quickTimeEpoch.Add(time.Duration(binary.BigEndian.Uint64(payload[4:12])) * time.Second)
		modified = quickTimeEpoch.Add(time.Duration(binary.BigEndian.Uint64(payload[12:20])) * time.Second)
	default:
		created = quickTimeEpoch.Add(time.Duration(binary.BigEndian.Uint32(payload[4:8])) * time.Second)
		modified = quickTimeEpoch.Add(time.Duration(binary.BigEndian.Uint32(payload[8:12])) * time.Second)
	}
	return created, modified, true
}

func boxPayload(data []byte, boxType string) ([]byte, bool) {
	offset := 0
	for offset+8 <= len(data) {
		size := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		headerSize := 8
		if size == 1 {
			if offset+16 > len(data) {
				break
			}
			size = int(binary.BigEndian.Uint64(data[offset+8 : offset+16]))
			headerSize = 16
		} else if size == 0 {
			size = len(data) - offset
		}
		if size < headerSize {
			break
		}
		end := offset + size
		if end > len(data) {
			end = len(data)
		}
		name := string(data[offset+4 : offset+8])
		if name == boxType {
			return data[offset+headerSize : end], true
		}
		if isContainerBox(name) {
			if inner, ok := boxPayload(data[offset+headerSize:end], boxType); ok {
				return inner, true
			}
		}
		if size <= 0 {
			break
		}
		offset += size
	}
	return nil, false
}

func isContainerBox(name string) bool {
	switch name {
	case "moov", "trak", "mdia", "minf", "stbl", "edts", "udta":
		return true
	}
	return false
}

// -------------------------------- PDF helpers ------------------------------

func pdfInfoObject(data []byte) int {
	s := string(data)
	re := regexp.MustCompile(`/Type\s*/Info\s*(\d+)\s+(\d+)\s+R`)
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return -1
}

func pdfObjectBytes(data []byte, num int) []byte {
	re := regexp.MustCompile(`(?s)` + strconv.Itoa(num) + `\s+0\s+obj(.*?)endobj`)
	m := re.FindSubmatch(data)
	if len(m) < 2 {
		return nil
	}
	return m[1]
}

func pdfParseInfo(dict []byte, res *Result) {
	keys := []struct {
		pdfKey string
		field  string
	}{
		{"Title", "title"}, {"Author", "author"}, {"Subject", "subject"},
		{"Creator", "creator"}, {"Producer", "producer"}, {"Keywords", "keywords"},
		{"CreationDate", "creation_date"}, {"ModDate", "modification_date"},
	}
	for _, k := range keys {
		re := regexp.MustCompile(`/` + k.pdfKey + `\s*\(((?:[^()\\]|\\.)*)\)`)
		m := re.FindSubmatch(dict)
		if len(m) >= 2 {
			if val := strings.TrimSpace(pdfUnescape(string(m[1]))); val != "" {
				addField(res, k.field, val)
				continue
			}
		}
		reHex := regexp.MustCompile(`/` + k.pdfKey + `\s*<([0-9A-Fa-f ]+)>`)
		if hm := reHex.FindSubmatch(dict); len(hm) >= 2 {
			clean := strings.ReplaceAll(string(hm[1]), " ", "")
			if len(clean)%2 == 0 {
				if b, err := hexDecode(clean); err == nil {
					if val := strings.TrimSpace(string(b)); val != "" {
						addField(res, k.field, val)
					}
				}
			}
		}
	}
}

func pdfUnescape(s string) string {
	s = strings.ReplaceAll(s, `\(`, `(`)
	s = strings.ReplaceAll(s, `\)`, `)`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	return s
}

func parsePDFDate(s string) (time.Time, bool) {
	if !strings.HasPrefix(s, "D:") {
		return time.Time{}, false
	}
	base := strings.TrimSpace(s[2:])
	for i, r := range base {
		if r == '+' || r == '-' || r == 'Z' {
			base = base[:i]
			break
		}
	}
	if len(base) < 14 {
		return time.Time{}, false
	}
	nums := make([]int, 6)
	for i, pair := range [][2]int{{0, 4}, {4, 6}, {6, 8}, {8, 10}, {10, 12}, {12, 14}} {
		n, err := strconv.Atoi(base[pair[0]:pair[1]])
		if err != nil {
			return time.Time{}, false
		}
		nums[i] = n
	}
	return time.Date(nums[0], time.Month(nums[1]), nums[2], nums[3], nums[4], nums[5], 0, time.UTC), true
}

func hexDecode(s string) ([]byte, error) {
	b := make([]byte, len(s)/2)
	for i := 0; i < len(b); i++ {
		hi, err := strconv.ParseUint(s[i*2:i*2+1], 16, 8)
		if err != nil {
			return nil, err
		}
		lo, err := strconv.ParseUint(s[i*2+1:i*2+2], 16, 8)
		if err != nil {
			return nil, err
		}
		b[i] = byte(hi<<4 | lo)
	}
	return b, nil
}

// ------------------------------ Archive helpers ----------------------------

// zipDocumentKind inspects the central directory and contents of a ZIP sample
// to distinguish OOXML, ODF and EPUB documents from a plain archive.
func zipDocumentKind(sample Sample) (string, bool) {
	data := sampleBytes(sample)
	if indexOf(data, []byte("[Content_Types].xml")) != -1 {
		return "OOXML", true
	}
	if m := zipFirstEntry(data, "mimetype"); len(m) > 0 {
		mt := strings.TrimSpace(string(m))
		switch {
		case strings.Contains(mt, "epub"):
			return "EPUB", true
		case strings.Contains(mt, "opendocument"):
			return "ODF", true
		}
	}
	if indexOf(data, []byte("docProps/core.xml")) != -1 {
		return "OOXML", true
	}
	if indexOf(data, []byte("container.xml")) != -1 {
		return "EPUB", true
	}
	if indexOf(data, []byte("content.xml")) != -1 {
		return "ODF", true
	}
	return "", false
}

// zipFirstEntry peeks at the first local file header of a ZIP to read an entry
// stored without compression (as ODF/EPUB's "mimetype" is).
func zipFirstEntry(data []byte, name string) []byte {
	if len(data) < 30 || string(data[:4]) != "PK\x03\x04" {
		return nil
	}
	fnameLen := int(binary.LittleEndian.Uint16(data[26:28]))
	if fnameLen < len(name) || fnameLen+30 > len(data) {
		return nil
	}
	if string(data[30:30+len(name)]) != name {
		return nil
	}
	compSize := int(binary.LittleEndian.Uint32(data[18:22]))
	end := 30 + fnameLen + compSize
	if end > len(data) {
		end = len(data)
	}
	return data[30+fnameLen : end]
}

// zipCoreProps scans a ZIP sample for the OOXML/ODF/EPUB core property XML and
// extracts title, creator and date fields.
func zipCoreProps(sample Sample) map[string]string {
	fields := make(map[string]string)
	data := sampleBytes(sample)
	core := regexp.MustCompile(`(?s)<(?:cp:)?coreProperties.*?</(?:cp:)?coreProperties>`)
	if m := core.Find(data); len(m) > 0 {
		pick := []struct {
			re    *regexp.Regexp
			field string
		}{
			{regexp.MustCompile(`(?s)<dc:title[^>]*>(.*?)</dc:title>`), "title"},
			{regexp.MustCompile(`(?s)<dc:subject[^>]*>(.*?)</dc:subject>`), "subject"},
			{regexp.MustCompile(`(?s)<dc:creator[^>]*>(.*?)</dc:creator>`), "creator"},
			{regexp.MustCompile(`(?s)<meta:initial-creator[^>]*>(.*?)</meta:initial-creator>`), "creator"},
			{regexp.MustCompile(`(?s)<dcterms:created[^>]*>(.*?)</dcterms:created>`), "created"},
			{regexp.MustCompile(`(?s)<dcterms:modified[^>]*>(.*?)</dcterms:modified>`), "modified"},
			{regexp.MustCompile(`(?s)<meta:creation-date[^>]*>(.*?)</meta:creation-date>`), "created"},
		}
		for _, p := range pick {
			if sm := p.re.FindSubmatch(m); len(sm) >= 2 {
				if v := strings.TrimSpace(string(sm[1])); v != "" {
					if _, exists := fields[p.field]; !exists {
						fields[p.field] = v
					}
				}
			}
		}
	}
	return fields
}

// ------------------------------- Audio helpers -----------------------------

func parseID3v2(data []byte, res *Result) {
	if len(data) < 10 || string(data[:3]) != "ID3" {
		return
	}
	size := int(data[6]&0x7f)<<21 | int(data[7]&0x7f)<<14 | int(data[8]&0x7f)<<7 | int(data[9]&0x7f)
	offset := 10
	if data[5]&0x40 != 0 && offset+4 <= len(data) { // extended header
		ext := int(data[offset])<<21 | int(data[offset+1])<<14 | int(data[offset+2])<<7 | int(data[offset+3])
		offset += 4 + ext
	}
	end := offset + size
	if end > len(data) {
		end = len(data)
	}
	for offset+10 <= end {
		frameID := string(data[offset : offset+4])
		if frameID == "\x00\x00\x00\x00" {
			break
		}
		frameSize := int(data[offset+4])<<24 | int(data[offset+5])<<16 | int(data[offset+6])<<8 | int(data[offset+7])
		if frameSize <= 0 {
			break
		}
		payload := data[offset+10 : min(offset+10+frameSize, end)]
		switch frameID {
		case "TIT2":
			addField(res, "title", id3Text(payload))
		case "TPE1":
			addField(res, "artist", id3Text(payload))
		case "TALB":
			addField(res, "album", id3Text(payload))
		case "TYER", "TDRC", "TDRL":
			if _, exists := res.Fields["creation_date"]; !exists {
				addField(res, "creation_date", id3Text(payload))
			}
		case "COMM", "TCOM":
			addField(res, "comment", id3Text(payload))
		}
		offset += 10 + frameSize
	}
}

func id3Text(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	raw := payload[1:] // skip text-encoding byte
	return strings.TrimSpace(strings.TrimRight(string(raw), "\x00"))
}

func parseID3v1(data []byte, res *Result) {
	if len(data) < 128 {
		return
	}
	addField(res, "title", strings.TrimRight(string(data[3:33]), "\x00 "))
	addField(res, "artist", strings.TrimRight(string(data[33:63]), "\x00 "))
	addField(res, "album", strings.TrimRight(string(data[63:93]), "\x00 "))
	if year := strings.TrimRight(string(data[93:97]), "\x00 "); year != "" {
		addField(res, "creation_date", year)
	}
}

func parseVorbisComments(data []byte, res *Result) {
	for _, b := range vorbisCommentBlocks(data) {
		for _, kv := range parseVorbisCommentBlock(b) {
			addField(res, kv[0], kv[1])
		}
	}
}

func vorbisCommentBlocks(data []byte) [][]byte {
	var blocks [][]byte
	if len(data) >= 4 && string(data[:4]) == "fLaC" {
		offset := 4
		for offset+4 <= len(data) {
			btype := data[offset]
			length := int(data[offset+1])<<16 | int(data[offset+2])<<8 | int(data[offset+3])
			offset += 4
			if offset+length > len(data) {
				break
			}
			if btype&0x7f == 4 {
				blocks = append(blocks, data[offset:offset+length])
			}
			if btype&0x80 != 0 {
				break
			}
			offset += length
		}
		return blocks
	}
	offset := 0
	for offset+27 <= len(data) && string(data[offset:offset+4]) == "OggS" {
		segCount := int(data[offset+26])
		segTable := offset + 27
		if segTable+segCount > len(data) {
			break
		}
		packetLen := 0
		pos := segTable
		for i := 0; i < segCount; i++ {
			packetLen += int(data[pos])
			pos++
		}
		packet := pos
		if packet+packetLen > len(data) {
			break
		}
		blk := data[packet : packet+packetLen]
		if idx := indexOf(blk, []byte{0x01, 'v', 'o', 'r', 'b', 'i', 's'}); idx >= 0 {
			blocks = append(blocks, blk[idx+7:])
		}
		offset = packet + packetLen
	}
	return blocks
}
func parseVorbisCommentBlock(b []byte) [][]string {
	var out [][]string
	if len(b) < 8 {
		return out
	}
	vlen := int(binary.LittleEndian.Uint32(b[0:4]))
	pos := 4 + vlen
	if pos+4 > len(b) {
		return out
	}
	count := int(binary.LittleEndian.Uint32(b[pos : pos+4]))
	pos += 4
	for i := 0; i < count && pos+4 <= len(b); i++ {
		clen := int(binary.LittleEndian.Uint32(b[pos : pos+4]))
		pos += 4
		if pos+clen > len(b) {
			break
		}
		comment := string(b[pos : pos+clen])
		pos += clen
		if eq := strings.Index(comment, "="); eq > 0 {
			key := vorbisField(strings.ToLower(strings.TrimSpace(comment[:eq])))
			val := strings.TrimSpace(comment[eq+1:])
			out = append(out, []string{key, val})
		}
	}
	return out
}

func vorbisField(key string) string {
	switch key {
	case "title", "artist", "album", "comment", "tracknumber", "genre":
		return key
	case "date":
		return "creation_date"
	}
	return key
}

func parseRIFFInfo(data []byte, res *Result) {
	if len(data) < 12 || string(data[:4]) != "RIFF" {
		return
	}
	offset := 12
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if offset+8+size > len(data) {
			break
		}
		if chunkID == "LIST" && size >= 4 && string(data[offset+8:offset+12]) == "INFO" {
			parseRIFFINFOList(data[offset+12:offset+8+size], res)
		}
		offset += 8 + size + (size & 1)
	}
}

func parseRIFFINFOList(payload []byte, res *Result) {
	mapping := map[string]string{
		"INAM": "title", "IART": "artist", "ICMT": "comment", "ICRD": "creation_date",
		"IPRD": "product", "ICOP": "copyright", "ISFT": "software", "IGNR": "genre",
		"IKEY": "keywords", "IENG": "engineer",
	}
	offset := 0
	for offset+8 <= len(payload) {
		id := string(payload[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(payload[offset+4 : offset+8]))
		if offset+8+size > len(payload) {
			break
		}
		if field, ok := mapping[id]; ok {
			if val := strings.TrimRight(string(payload[offset+8:offset+8+size]), "\x00 "); val != "" {
				addField(res, field, val)
			}
		}
		offset += 8 + size + (size & 1)
	}
}

func parseAudioDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04:05", "20060102", "2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseISO8601(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
