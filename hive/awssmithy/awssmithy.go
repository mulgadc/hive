package awssmithy

type FilterList struct {
	Name   string
	Values []string
}

type DescribeInstancesRequest struct {
	InstanceIds []string     `xml:"InstanceId"`
	DryRun      bool         `xml:"DryRun"`
	Filters     []FilterList `xml:"Filter"`

	NextToken string `xml:"nextToken"`

	MaxResults int `xml:"maxResults"`
}
