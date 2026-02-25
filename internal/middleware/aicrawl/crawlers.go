package aicrawl

import "regexp"

// CrawlerInfo describes a known AI crawler.
type CrawlerInfo struct {
	Name        string
	Pattern     *regexp.Regexp
	Description string
}

// BuiltinCrawlers is the list of known AI crawlers with pre-compiled patterns.
// Last updated: 2026-02
var BuiltinCrawlers = []*CrawlerInfo{
	{Name: "GPTBot", Pattern: regexp.MustCompile(`(?i)GPTBot`), Description: "OpenAI training crawler"},
	{Name: "ChatGPT-User", Pattern: regexp.MustCompile(`(?i)ChatGPT-User`), Description: "OpenAI ChatGPT browsing"},
	{Name: "ClaudeBot", Pattern: regexp.MustCompile(`(?i)ClaudeBot`), Description: "Anthropic training crawler"},
	{Name: "anthropic-ai", Pattern: regexp.MustCompile(`(?i)anthropic-ai`), Description: "Anthropic AI agent"},
	{Name: "Google-Extended", Pattern: regexp.MustCompile(`(?i)Google-Extended`), Description: "Google AI training"},
	{Name: "CCBot", Pattern: regexp.MustCompile(`(?i)CCBot`), Description: "Common Crawl"},
	{Name: "Bytespider", Pattern: regexp.MustCompile(`(?i)Bytespider`), Description: "ByteDance/TikTok"},
	{Name: "Applebot-Extended", Pattern: regexp.MustCompile(`(?i)Applebot-Extended`), Description: "Apple AI training"},
	{Name: "PerplexityBot", Pattern: regexp.MustCompile(`(?i)PerplexityBot`), Description: "Perplexity AI search"},
	{Name: "Amazonbot", Pattern: regexp.MustCompile(`(?i)Amazonbot`), Description: "Amazon AI"},
	{Name: "FacebookBot", Pattern: regexp.MustCompile(`(?i)FacebookBot`), Description: "Meta AI"},
	{Name: "cohere-ai", Pattern: regexp.MustCompile(`(?i)cohere-ai`), Description: "Cohere AI"},
	{Name: "Diffbot", Pattern: regexp.MustCompile(`(?i)Diffbot`), Description: "Diffbot extraction"},
	{Name: "Timpibot", Pattern: regexp.MustCompile(`(?i)Timpibot`), Description: "Timpi search"},
	{Name: "ImagesiftBot", Pattern: regexp.MustCompile(`(?i)ImagesiftBot`), Description: "Imagesift AI"},
	{Name: "Omgilibot", Pattern: regexp.MustCompile(`(?i)Omgilibot`), Description: "Omgili data crawler"},
	{Name: "AI2Bot", Pattern: regexp.MustCompile(`(?i)AI2Bot`), Description: "Allen AI"},
}

// BuiltinByName provides O(1) lookup of built-in crawlers by name.
var BuiltinByName map[string]*CrawlerInfo

func init() {
	BuiltinByName = make(map[string]*CrawlerInfo, len(BuiltinCrawlers))
	for _, c := range BuiltinCrawlers {
		BuiltinByName[c.Name] = c
	}
}
