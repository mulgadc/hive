package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/davecgh/go-spew/spew"
)

// Parse AWS Smithy models

type Smithy struct {
	Smithy   string `json:"smithy"`
	Metadata map[string]interface{}
	Shapes   map[string]interface{}
}

type SmithyRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type SmithyPaginated struct {
	InputToken  string `json:"inputToken"`
	OutputToken string `json:"outputToken"`
	Items       string `json:"items"`
	PageSize    string `json:"pageSize"`
}

type SmithyTrait struct {
	Documentation string `json:"smithy.api#documentation"`
	XMLName       string `json:"smithy.api#xmlName"`
	Ec2QueryName  string `json:"smithy.api#ec2QueryName"`

	Range           SmithyRange     `json:"smithy.api#range"`     // optional
	SmithyPaginated SmithyPaginated `json:"smithy.api#paginated"` // optional

}

type SmithyMember struct {
	Target string      `json:"target"`
	Traits SmithyTrait `json:"traits"`
}

type SmithyMemberStructure struct {
	Target string      `json:"target"`
	Traits SmithyTrait `json:"traits"`
}

type SmithyRequest struct {
	Type string `json:"type"`
}

type SmithyRequestList struct {
	Type   string       `json:"type"`
	Member SmithyMember `json:"member"`
}

type SmithyRequestStructure struct {
	Type    string                           `json:"type"`
	Members map[string]SmithyMemberStructure `json:"members"`
	Traits  SmithyTrait                      `json:"traits"`
}

var result Smithy

var smithyTypes = map[string]string{
	"String":   "string",
	"Integer":  "int",
	"Boolean":  "bool",
	"DateTime": "time.Time",
}

func main() {

	path := flag.String("path", "", "Path to the Smithy model")
	flag.Parse()

	if path == nil {
		log.Fatal("path is required")
	}

	// Open the file and read JSON
	jsonFile, err := os.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer jsonFile.Close()

	// Read the file and unmarshal as JSON
	byteValue, _ := io.ReadAll(jsonFile)

	json.Unmarshal([]byte(byteValue), &result)

	//	spew.Dump(result)

	_, req := findShape("com.amazonaws.ec2#DescribeInstancesRequest")
	scanRequest("com.amazonaws.ec2#DescribeInstancesRequest", req, true)

	/*
		for k, v := range result.Shapes {

			// Find all request methods

			// "com.amazonaws.ec2#RunInstancesRequest"
			// DescribeInstancesRequest
			if strings.HasSuffix(k, "DescribeInstancesRequest") {

				//fmt.Println(k)

				scanRequest(k, v.(interface{}), true)

				// Loop in each request
				//for k2, v2 := range v.(map[string]interface{}) {
				//	fmt.Println(k2, v2)
				//}

			}

		}
	*/
}

func scanRequest(key string, request interface{}, root bool) {

	//spew.Dump(request)
	// Marshal into a SmithyRequest

	// Unmarshal into a SmithyRequest

	var smithyRequest SmithyRequest

	b, _ := json.Marshal(request)         // map -> JSON
	_ = json.Unmarshal(b, &smithyRequest) // JSON -> struct

	//spew.Dump(smithyRequest)

	keyName := strings.Split(key, "#")

	if len(keyName) == 0 {
		fmt.Println("No key found")
		return
	}

	if root {
		fmt.Printf("type %s struct {\n", keyName[1])
	}

	if smithyRequest.Type == "structure" {
		findShapeRecursiveStructure(request)
	} else if smithyRequest.Type == "list" {
		findShapeRecursiveList(request)
	}

	//fmt.Println(smithyRequest.Type)

	//for k, v := range request {
	//	fmt.Println(k, v)
	//}

}

func findShapeRecursiveList(request interface{}) error {

	var smithyRequest SmithyRequestList

	b, _ := json.Marshal(request)         // map -> JSON
	_ = json.Unmarshal(b, &smithyRequest) // JSON -> struct

	fmt.Println("findShapeRecursiveList:")
	spew.Dump(request)
	spew.Dump(smithyRequest)

	memberType := strings.Split(smithyRequest.Member.Target, "#")

	fmt.Println(memberType)

	if len(memberType) == 0 {
		fmt.Println("No member type found")
		return nil
	}

	typeName := memberType[1]

	// If a matching smithyType
	goType := smithyTypes[typeName]

	if goType != "" {
		fmt.Printf("%s `xml:\"%s\"`\n", goType, smithyRequest.Member.Traits.XMLName)

	} else {
		//spew.Dump(v)
		fmt.Println("\t", smithyRequest.Member.Target, smithyRequest.Member.Traits.XMLName)

		newSmithyRequest, request := findShape(smithyRequest.Member.Target)

		if newSmithyRequest.Type == "structure" {
			findShapeRecursiveStructure(request)
		} else if newSmithyRequest.Type == "list" {
			findShapeRecursiveList(request)
		}

		//findShapeRecursiveStructure(newSmithyRequest)

	}

	//fmt.Println(smithyRequest.Members)

	fmt.Println()

	return nil

}

func findShapeRecursiveStructure(request interface{}) error {

	fmt.Println("findShapeRecursiveStructure => \t")

	var smithyRequest SmithyRequestStructure

	b, _ := json.Marshal(request)         // map -> JSON
	_ = json.Unmarshal(b, &smithyRequest) // JSON -> struct

	for k, v := range smithyRequest.Members {

		//fmt.Println("\t", k, v)

		memberType := strings.Split(v.Target, "#")

		fmt.Println(memberType)

		if len(memberType) == 0 {
			fmt.Println("No member type found")
			continue
		}

		typeName := memberType[1]

		// If a matching smithyType
		goType := smithyTypes[typeName]

		if goType != "" {
			fmt.Printf("%s %s `xml:\"%s\"`\n", k, goType, v.Traits.XMLName)

		} else {
			//spew.Dump(v)
			//fmt.Println("\t", k, v.Target, v.Traits.XMLName)

			newSmithyRequest, request := findShape(v.Target)

			if newSmithyRequest.Type == "structure" {
				findShapeRecursiveStructure(request)
			} else if newSmithyRequest.Type == "list" {
				findShapeRecursiveList(request)
			}
			//findShapeRecursiveStructure(newSmithyRequest)

		}

	}

	//fmt.Println(smithyRequest.Members)

	fmt.Println()

	return nil

}

func findShape(key string) (SmithyRequest, interface{}) {

	for k, v := range result.Shapes {
		if k == key {

			//fmt.Println("MATCH", k, key)

			//spew.Dump(v)
			//fmt.Println(k, v)
			var smithyRequest SmithyRequest

			b, _ := json.Marshal(v)               // map -> JSON
			_ = json.Unmarshal(b, &smithyRequest) // JSON -> struct

			//spew.Dump(v)

			//spew.Dump(smithyRequest)

			return smithyRequest, v

		}
	}

	return SmithyRequest{}, nil

}
