package server

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/compliance"
	"github.com/projectcalico/libcalico-go/lib/errors"
	"github.com/projectcalico/libcalico-go/lib/set"

	"github.com/tigera/compliance/pkg/report"
)

// handleDownloadReports sends one or multiple (via zip) reports to the client
func (s *server) handleDownloadReports(response http.ResponseWriter, request *http.Request) {
	// Determine the download formats and if there were none set then exit immediately.
	formats := request.URL.Query()[QueryFormat]
	log.WithField("Formats", formats).Info("Extracted download formats from URL")

	// Determine the report UID. The pattern MUX will have extracted this parameter from the URL.
	uid := request.URL.Query().Get(QueryReport)
	log.WithField("ReportUID", uid).Info("Extracted report UID from URL")

	// Download the report.
	r, err := s.rr.RetrieveArchivedReport(uid)
	if err != nil {
		if _, ok := err.(errors.ErrorResourceDoesNotExist); ok {
			http.Error(response, "Report does not exist", http.StatusNotFound)
			return
		}
		http.Error(response, "Unable to download report", http.StatusServiceUnavailable)
	}

	// Create an RBAC helper to see if we can download this report
	rbac := s.rhf.NewReportRbacHelper(request)
	allow, err := rbac.CanViewReport(r.ReportTypeName, r.ReportName)
	if err != nil {
		log.WithError(err).Error("Unable to determine access permissions for request")
		http.Error(response, err.Error(), http.StatusServiceUnavailable)
		return
	}

	// Deny if not allowed access to report
	if !allow {
		log.Debug("Requester has insufficient permissions to view report")
		http.Error(response, "Access denied", http.StatusForbidden)
		return
	}

	// Obtain the current set of configured ReportTypes.
	rts, err := s.getReportTypes()
	if err != nil {
		log.WithError(err).Error("Unable to query report types")
		http.Error(response, err.Error(), http.StatusServiceUnavailable)
		return
	}
	rt, ok := rts[r.ReportTypeName]
	// ReportType is deleted, use ReportTypeSpec in the ReportData.
	if !ok {
		rt = &r.ReportTypeSpec
		log.Infof("ReportType (%s) deleted from the configuration, using from ReportData", r.ReportTypeName)
	}

	// Check that the formats are valid.
	if !areValidFormats(formats, rt) {
		log.WithError(err).Info("Requested format is not valid for the ReportType")
		http.Error(response, "Requested format is not valid for the report type", http.StatusBadRequest)
		return
	}

	// Prepare the report for download.
	dc, err := s.prepareReportForDownload(r, uid, formats, rt)
	if err != nil {
		log.WithError(err).Info("Unable to fulfill the request")
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}

	// Determine the download filename. This will depend whether it is a single file or multiple file zipped up.
	fileName := dc.generateDownloadFileName()
	log.WithField("Filename", fileName).Debug("Setting download filename")

	//set the response header and send the file
	response.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	response.Header().Set("Content-Type", dc.contentType())
	byteContent, err := dc.content()
	if err != nil {
		log.WithError(err).Info("Error generating file content")
		http.Error(response, err.Error(), http.StatusInternalServerError)
	}

	http.ServeContent(response, request, fileName, time.Now(), bytes.NewReader(byteContent))
}

func (s *server) prepareReportForDownload(
	r *report.ArchivedReportData, uid string, formats []string, rt *v3.ReportTypeSpec,
) (*downloadContent, error) {
	// Init the download content.
	dc := &downloadContent{
		startDate:  r.StartTime,
		endDate:    r.EndTime,
		reportName: r.ReportName,
		reportType: r.ReportTypeName,
	}

	// Extract the templates by name.
	templates := make(map[string]v3.ReportTemplate)
	for idx := range rt.DownloadTemplates {
		templates[rt.DownloadTemplates[idx].Name] = rt.DownloadTemplates[idx]
	}

	handled := set.New()
	for _, format := range formats {
		if handled.Contains(format) {
			// Handle de-duplication of the formats.
			continue
		}
		handled.Add(format)

		// We know the template exists - it's already been checked.
		template := templates[format]

		renderedReport, err := compliance.RenderTemplate(template.Template, r.ReportData)
		if err != nil {
			log.WithError(err).Infof("Error rendering from template: %s", template.Name)
			return nil, err
		}
		dc.files = append(dc.files, downloadFile{
			contentType:  "text/plain",
			data:         []byte(renderedReport),
			outputFormat: format,
		})
	}

	return dc, nil
}

type downloadContent struct {
	startDate  metav1.Time
	endDate    metav1.Time
	reportType string
	reportName string
	files      []downloadFile
}

func (d *downloadContent) contentType() string {
	if len(d.files) == 1 {
		return d.files[0].contentType
	}
	return "application/zip"
}

func (d *downloadContent) generateDownloadFileName() string {
	if len(d.files) == 1 {
		return generateFileName(d, d.files[0].outputFormat)
	}
	return generateFileName(d, ".zip")
}

func (d *downloadContent) content() ([]byte, error) {
	if len(d.files) == 1 {
		return []byte(d.files[0].data), nil
	}
	return d.zipContent()
}

type downloadFile struct {
	outputFormat string
	contentType  string
	data         []byte
}

const fileNameTimeFormat = "20060102150405"

//generates our report download file name
func generateFileName(dc *downloadContent, outputFormat string) string {
	startDateStr := dc.startDate.Format(fileNameTimeFormat)
	endDateStr := dc.endDate.Format(fileNameTimeFormat)
	var fileName string
	if strings.HasPrefix(outputFormat, ".") {
		fileName = fmt.Sprintf("%s_%s_%s-%s%s", dc.reportType, dc.reportName, startDateStr, endDateStr, outputFormat)
	} else {
		fileName = fmt.Sprintf("%s_%s_%s-%s-%s", dc.reportType, dc.reportName, startDateStr, endDateStr, outputFormat)
	}
	return fileName
}

// areValidFormats returns true if all formats passed are valid for the ReportType.
func areValidFormats(formats []string, rt *v3.ReportTypeSpec) bool {
	valid := set.New()
	for idx := range rt.DownloadTemplates {
		valid.Add(rt.DownloadTemplates[idx].Name)
	}
	for _, format := range formats {
		if !valid.Contains(format) {
			log.WithField("Format", format).Info("Requested download format is not valid for the report type")
			return false
		}
	}

	return true
}

func (d *downloadContent) zipContent() ([]byte, error) {

	//set up the zipwriter
	var b bytes.Buffer
	zipWriter := zip.NewWriter(&b)

	for _, f := range d.files {

		//create the fileheader
		fh := zip.FileHeader{
			Method: zip.Deflate,
			Name:   generateFileName(d, f.outputFormat),
		}

		//create the next header
		writer, err := zipWriter.CreateHeader(&fh)
		if err != nil {
			log.WithError(err).Error("Unable to create zip file header")
			return nil, err
		}

		//wrap the current file data in a reader and copy to the writer
		contentReader := bytes.NewReader(f.data)
		_, err = io.Copy(writer, contentReader)
		if err != nil {
			log.WithError(err).Error("Unable to write file content to zip")
			return nil, err
		}

	}

	err := zipWriter.Close()
	if err != nil {
		log.WithError(err).Error("Unable to close zip writer")
		return nil, err
	}

	//return the zip data
	return b.Bytes(), nil
}
