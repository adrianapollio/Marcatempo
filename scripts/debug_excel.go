package main

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"
)

type SstXML struct {
	XMLName xml.Name `xml:"sst"`
	Si      []struct {
		T string `xml:"t"`
	} `xml:"si"`
}

type WorksheetXML struct {
	XMLName   xml.Name `xml:"worksheet"`
	SheetData struct {
		Row []struct {
			R int `xml:"r,attr"`
			C []struct {
				R  string `xml:"r,attr"`
				T  string `xml:"t,attr"`
				V  string `xml:"v"`
				Is struct {
					T string `xml:"t"`
				} `xml:"is"`
			} `xml:"c"`
		} `xml:"row"`
	} `xml:"sheetData"`
}

func main() {
	fileName := "20260316_R(1).xlsx"
	if len(os.Args) > 1 {
		fileName = os.Args[1]
	}

	r, err := zip.OpenReader(fileName)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	defer r.Close()

	var sharedStrings SstXML
	var worksheet WorksheetXML

	for _, f := range r.File {
		name := strings.ToLower(f.Name)
		if name == "xl/sharedstrings.xml" {
			rc, err := f.Open()
			if err == nil {
				data, _ := ioutil.ReadAll(rc)
				xml.Unmarshal(data, &sharedStrings)
				rc.Close()
			}
		}
		if name == "xl/worksheets/sheet1.xml" {
			rc, err := f.Open()
			if err == nil {
				data, _ := ioutil.ReadAll(rc)
				xml.Unmarshal(data, &worksheet)
				rc.Close()
			}
		}
	}

	fmt.Printf("Dumping first 10 rows of %s:\n", fileName)
	for i, row := range worksheet.SheetData.Row {
		if i >= 10 {
			break
		}
		var rowData []string
		for _, col := range row.C {
			val := col.V
			if col.T == "s" {
				idx, _ := strconv.Atoi(val)
				if idx >= 0 && idx < len(sharedStrings.Si) {
					val = sharedStrings.Si[idx].T
				}
			} else if col.T == "inlineStr" {
				val = col.Is.T
			}
			rowData = append(rowData, val)
		}
		fmt.Printf("Row %d: %v\n", i+1, rowData)
	}
}
