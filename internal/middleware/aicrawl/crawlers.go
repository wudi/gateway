package aicrawl

import "regexp"

// CrawlerInfo describes a known AI crawler.
type CrawlerInfo struct {
	Name        string
	Pattern     *regexp.Regexp
	Description string
}

// BuiltinCrawlers is the list of known AI crawlers with pre-compiled patterns.
// Source: https://github.com/ai-robots-txt/ai.robots.txt
// Last updated: 2026-02
var BuiltinCrawlers = []*CrawlerInfo{
	// OpenAI
	{Name: "GPTBot", Pattern: regexp.MustCompile(`(?i)GPTBot`), Description: "OpenAI training crawler"},
	{Name: "ChatGPT-User", Pattern: regexp.MustCompile(`(?i)ChatGPT-User`), Description: "OpenAI ChatGPT browsing"},
	{Name: "OAI-SearchBot", Pattern: regexp.MustCompile(`(?i)OAI-SearchBot`), Description: "OpenAI search crawler"},
	{Name: "ChatGPT-Agent", Pattern: regexp.MustCompile(`(?i)ChatGPT[\s-]Agent`), Description: "OpenAI ChatGPT agent"},
	{Name: "Operator", Pattern: regexp.MustCompile(`(?i)Operator`), Description: "OpenAI Operator agent"},

	// Anthropic
	{Name: "ClaudeBot", Pattern: regexp.MustCompile(`(?i)ClaudeBot`), Description: "Anthropic training crawler"},
	{Name: "anthropic-ai", Pattern: regexp.MustCompile(`(?i)anthropic-ai`), Description: "Anthropic AI agent"},
	{Name: "Claude-SearchBot", Pattern: regexp.MustCompile(`(?i)Claude-SearchBot`), Description: "Anthropic search crawler"},
	{Name: "Claude-User", Pattern: regexp.MustCompile(`(?i)Claude-User`), Description: "Anthropic Claude user agent"},
	{Name: "Claude-Web", Pattern: regexp.MustCompile(`(?i)Claude-Web`), Description: "Anthropic Claude web browsing"},

	// Google
	{Name: "Google-Extended", Pattern: regexp.MustCompile(`(?i)Google-Extended`), Description: "Google AI training"},
	{Name: "GoogleOther", Pattern: regexp.MustCompile(`(?i)GoogleOther`), Description: "Google other AI crawlers"},
	{Name: "CloudVertexBot", Pattern: regexp.MustCompile(`(?i)CloudVertexBot`), Description: "Google Cloud Vertex AI"},
	{Name: "NotebookLM", Pattern: regexp.MustCompile(`(?i)NotebookLM`), Description: "Google NotebookLM"},
	{Name: "Google-Firebase", Pattern: regexp.MustCompile(`(?i)Google-Firebase`), Description: "Google Firebase AI"},
	{Name: "GoogleAgent-Mariner", Pattern: regexp.MustCompile(`(?i)GoogleAgent-Mariner`), Description: "Google Mariner agent"},
	{Name: "Gemini-Deep-Research", Pattern: regexp.MustCompile(`(?i)Gemini-Deep-Research`), Description: "Google Gemini deep research"},

	// Apple
	{Name: "Applebot-Extended", Pattern: regexp.MustCompile(`(?i)Applebot-Extended`), Description: "Apple AI training"},

	// Meta
	{Name: "FacebookBot", Pattern: regexp.MustCompile(`(?i)FacebookBot`), Description: "Meta AI"},
	{Name: "facebookexternalhit", Pattern: regexp.MustCompile(`(?i)facebookexternalhit`), Description: "Meta external fetcher"},
	{Name: "Meta-ExternalAgent", Pattern: regexp.MustCompile(`(?i)Meta-ExternalAgent`), Description: "Meta AI external agent"},
	{Name: "Meta-ExternalFetcher", Pattern: regexp.MustCompile(`(?i)Meta-ExternalFetcher`), Description: "Meta AI external fetcher"},
	{Name: "meta-webindexer", Pattern: regexp.MustCompile(`(?i)meta-webindexer`), Description: "Meta web indexer"},

	// Amazon
	{Name: "Amazonbot", Pattern: regexp.MustCompile(`(?i)Amazonbot`), Description: "Amazon AI"},
	{Name: "AmazonBuyForMe", Pattern: regexp.MustCompile(`(?i)AmazonBuyForMe`), Description: "Amazon Buy For Me agent"},
	{Name: "Amzn-SearchBot", Pattern: regexp.MustCompile(`(?i)Amzn-SearchBot`), Description: "Amazon search bot"},
	{Name: "amazon-kendra", Pattern: regexp.MustCompile(`(?i)amazon-kendra`), Description: "Amazon Kendra AI search"},
	{Name: "bedrockbot", Pattern: regexp.MustCompile(`(?i)bedrockbot`), Description: "AWS Bedrock AI"},

	// ByteDance / TikTok
	{Name: "Bytespider", Pattern: regexp.MustCompile(`(?i)Bytespider`), Description: "ByteDance/TikTok"},
	{Name: "PetalBot", Pattern: regexp.MustCompile(`(?i)PetalBot`), Description: "ByteDance Petal search"},
	{Name: "TikTokSpider", Pattern: regexp.MustCompile(`(?i)TikTokSpider`), Description: "TikTok spider"},

	// Microsoft / Azure
	{Name: "AzureAI-SearchBot", Pattern: regexp.MustCompile(`(?i)AzureAI-SearchBot`), Description: "Azure AI search"},

	// Cohere
	{Name: "cohere-ai", Pattern: regexp.MustCompile(`(?i)cohere-ai`), Description: "Cohere AI"},
	{Name: "cohere-training-data-crawler", Pattern: regexp.MustCompile(`(?i)cohere-training-data-crawler`), Description: "Cohere training data crawler"},

	// DeepSeek
	{Name: "DeepSeekBot", Pattern: regexp.MustCompile(`(?i)DeepSeekBot`), Description: "DeepSeek AI"},

	// Mistral
	{Name: "MistralAI-User", Pattern: regexp.MustCompile(`(?i)MistralAI-User`), Description: "Mistral AI user agent"},

	// Perplexity
	{Name: "PerplexityBot", Pattern: regexp.MustCompile(`(?i)PerplexityBot`), Description: "Perplexity AI search"},
	{Name: "Perplexity-User", Pattern: regexp.MustCompile(`(?i)Perplexity-User`), Description: "Perplexity user agent"},

	// Common Crawl
	{Name: "CCBot", Pattern: regexp.MustCompile(`(?i)CCBot`), Description: "Common Crawl"},

	// Diffbot
	{Name: "Diffbot", Pattern: regexp.MustCompile(`(?i)Diffbot`), Description: "Diffbot extraction"},

	// AI search engines and assistants
	{Name: "DuckAssistBot", Pattern: regexp.MustCompile(`(?i)DuckAssistBot`), Description: "DuckDuckGo AI assistant"},
	{Name: "Bravebot", Pattern: regexp.MustCompile(`(?i)Bravebot`), Description: "Brave search AI"},
	{Name: "YouBot", Pattern: regexp.MustCompile(`(?i)YouBot`), Description: "You.com AI search"},
	{Name: "PhindBot", Pattern: regexp.MustCompile(`(?i)PhindBot`), Description: "Phind AI search"},
	{Name: "Andibot", Pattern: regexp.MustCompile(`(?i)Andibot`), Description: "Andi AI search"},
	{Name: "iAskBot", Pattern: regexp.MustCompile(`(?i)iAskBot`), Description: "iAsk AI search"},
	{Name: "iaskspider", Pattern: regexp.MustCompile(`(?i)iaskspider`), Description: "iAsk AI spider"},
	{Name: "kagi-fetcher", Pattern: regexp.MustCompile(`(?i)kagi-fetcher`), Description: "Kagi search fetcher"},
	{Name: "LinkupBot", Pattern: regexp.MustCompile(`(?i)LinkupBot`), Description: "Linkup AI search"},

	// AI coding / agents
	{Name: "Devin", Pattern: regexp.MustCompile(`(?i)\bDevin\b`), Description: "Cognition AI Devin"},
	{Name: "NovaAct", Pattern: regexp.MustCompile(`(?i)NovaAct`), Description: "Amazon Nova AI agent"},
	{Name: "Manus-User", Pattern: regexp.MustCompile(`(?i)Manus-User`), Description: "Manus AI agent"},
	{Name: "TwinAgent", Pattern: regexp.MustCompile(`(?i)TwinAgent`), Description: "Twin AI agent"},

	// Web scraping / AI data collection
	{Name: "Crawl4AI", Pattern: regexp.MustCompile(`(?i)Crawl4AI`), Description: "Crawl4AI scraper"},
	{Name: "FirecrawlAgent", Pattern: regexp.MustCompile(`(?i)FirecrawlAgent`), Description: "Firecrawl AI scraper"},
	{Name: "img2dataset", Pattern: regexp.MustCompile(`(?i)img2dataset`), Description: "LAION image dataset scraper"},
	{Name: "LAIONDownloader", Pattern: regexp.MustCompile(`(?i)LAIONDownloader`), Description: "LAION dataset downloader"},
	{Name: "Crawlspace", Pattern: regexp.MustCompile(`(?i)Crawlspace`), Description: "Crawlspace AI scraper"},

	// Yandex
	{Name: "YandexAdditionalBot", Pattern: regexp.MustCompile(`(?i)YandexAdditional`), Description: "Yandex AI training"},

	// Chinese AI
	{Name: "ChatGLM-Spider", Pattern: regexp.MustCompile(`(?i)ChatGLM-Spider`), Description: "Zhipu AI ChatGLM"},
	{Name: "PanguBot", Pattern: regexp.MustCompile(`(?i)PanguBot`), Description: "Huawei PanGu AI"},
	{Name: "SBIntuitionsBot", Pattern: regexp.MustCompile(`(?i)SBIntuitionsBot`), Description: "SB Intuitions AI"},

	// AI content tools
	{Name: "QuillBot", Pattern: regexp.MustCompile(`(?i)QuillBot|quillbot\.com`), Description: "QuillBot AI writing"},
	{Name: "LinerBot", Pattern: regexp.MustCompile(`(?i)LinerBot`), Description: "Liner AI highlighter"},
	{Name: "WRTNBot", Pattern: regexp.MustCompile(`(?i)WRTNBot`), Description: "WRTN AI"},
	{Name: "TavilyBot", Pattern: regexp.MustCompile(`(?i)TavilyBot`), Description: "Tavily AI search API"},
	{Name: "Thinkbot", Pattern: regexp.MustCompile(`(?i)Thinkbot`), Description: "Thinkbot AI"},

	// SEO / Marketing AI
	{Name: "SemrushBot-AI", Pattern: regexp.MustCompile(`(?i)SemrushBot-(?:OCOB|SWA)`), Description: "Semrush AI crawlers"},
	{Name: "Awario", Pattern: regexp.MustCompile(`(?i)Awario`), Description: "Awario social listening"},
	{Name: "KlaviyoAIBot", Pattern: regexp.MustCompile(`(?i)KlaviyoAIBot`), Description: "Klaviyo AI marketing"},
	{Name: "QualifiedBot", Pattern: regexp.MustCompile(`(?i)QualifiedBot`), Description: "Qualified AI sales"},

	// Data / research crawlers
	{Name: "Timpibot", Pattern: regexp.MustCompile(`(?i)Timpibot`), Description: "Timpi search"},
	{Name: "ImagesiftBot", Pattern: regexp.MustCompile(`(?i)ImagesiftBot`), Description: "Imagesift AI"},
	{Name: "Omgilibot", Pattern: regexp.MustCompile(`(?i)Omgilibot`), Description: "Omgili data crawler"},
	{Name: "AI2Bot", Pattern: regexp.MustCompile(`(?i)AI2Bot`), Description: "Allen AI"},
	{Name: "Factset_spyderbot", Pattern: regexp.MustCompile(`(?i)Factset_spyderbot`), Description: "FactSet financial data"},
	{Name: "ISSCyberRiskCrawler", Pattern: regexp.MustCompile(`(?i)ISSCyberRiskCrawler`), Description: "ISS cyber risk assessment"},
	{Name: "ICC-Crawler", Pattern: regexp.MustCompile(`(?i)ICC-Crawler`), Description: "ICC data crawler"},
	{Name: "Panscient", Pattern: regexp.MustCompile(`(?i)Panscient`), Description: "Panscient research crawler"},
	{Name: "KunatoCrawler", Pattern: regexp.MustCompile(`(?i)KunatoCrawler`), Description: "Kunato AI valuation"},

	// Cloudflare
	{Name: "Cloudflare-AutoRAG", Pattern: regexp.MustCompile(`(?i)Cloudflare-AutoRAG`), Description: "Cloudflare AutoRAG"},

	// Atlassian
	{Name: "atlassian-bot", Pattern: regexp.MustCompile(`(?i)atlassian-bot`), Description: "Atlassian AI bot"},

	// Other AI bots
	{Name: "Anomura", Pattern: regexp.MustCompile(`(?i)Anomura`), Description: "Anomura AI"},
	{Name: "AddSearchBot", Pattern: regexp.MustCompile(`(?i)AddSearchBot`), Description: "AddSearch AI"},
	{Name: "aiHitBot", Pattern: regexp.MustCompile(`(?i)aiHitBot`), Description: "aiHit AI"},
	{Name: "bigsur.ai", Pattern: regexp.MustCompile(`(?i)bigsur\.ai`), Description: "BigSur AI"},
	{Name: "Brightbot", Pattern: regexp.MustCompile(`(?i)Brightbot`), Description: "Bright Data AI"},
	{Name: "BuddyBot", Pattern: regexp.MustCompile(`(?i)BuddyBot`), Description: "Buddy AI"},
	{Name: "Channel3Bot", Pattern: regexp.MustCompile(`(?i)Channel3Bot`), Description: "Channel3 AI"},
	{Name: "Cotoyogi", Pattern: regexp.MustCompile(`(?i)Cotoyogi`), Description: "Cotoyogi AI"},
	{Name: "EchoboxBot", Pattern: regexp.MustCompile(`(?i)Echobox|Echobot`), Description: "Echobox AI"},
	{Name: "FriendlyCrawler", Pattern: regexp.MustCompile(`(?i)FriendlyCrawler`), Description: "FriendlyCrawler AI"},
	{Name: "IbouBot", Pattern: regexp.MustCompile(`(?i)IbouBot`), Description: "Ibou AI"},
	{Name: "imageSpider", Pattern: regexp.MustCompile(`(?i)imageSpider`), Description: "Image spider AI"},
	{Name: "Kangaroo-Bot", Pattern: regexp.MustCompile(`(?i)Kangaroo Bot`), Description: "Kangaroo AI"},
	{Name: "Linguee-Bot", Pattern: regexp.MustCompile(`(?i)Linguee Bot`), Description: "DeepL/Linguee AI"},
	{Name: "MyCentralAIScraperBot", Pattern: regexp.MustCompile(`(?i)MyCentralAIScraperBot`), Description: "MyCentral AI scraper"},
	{Name: "netEstate", Pattern: regexp.MustCompile(`(?i)netEstate`), Description: "netEstate crawler"},
	{Name: "Poggio-Citations", Pattern: regexp.MustCompile(`(?i)Poggio-Citations`), Description: "Poggio citations crawler"},
	{Name: "Poseidon-Research", Pattern: regexp.MustCompile(`(?i)Poseidon Research`), Description: "Poseidon research crawler"},
	{Name: "ShapBot", Pattern: regexp.MustCompile(`(?i)ShapBot`), Description: "Shap AI"},
	{Name: "Sidetrade", Pattern: regexp.MustCompile(`(?i)Sidetrade`), Description: "Sidetrade AI indexer"},
	{Name: "TerraCotta", Pattern: regexp.MustCompile(`(?i)TerraCotta`), Description: "TerraCotta AI"},
	{Name: "VelenPublicWebCrawler", Pattern: regexp.MustCompile(`(?i)VelenPublicWebCrawler`), Description: "Velen public web crawler"},
	{Name: "WARDBot", Pattern: regexp.MustCompile(`(?i)WARDBot`), Description: "WARD AI bot"},
	{Name: "Webzio-Extended", Pattern: regexp.MustCompile(`(?i)Webzio-Extended`), Description: "Webzio AI extended"},
	{Name: "ZanistaBot", Pattern: regexp.MustCompile(`(?i)ZanistaBot`), Description: "Zanista AI"},
	{Name: "Datenbank-Crawler", Pattern: regexp.MustCompile(`(?i)Datenbank Crawler`), Description: "Datenbank AI crawler"},
}

// BuiltinByName provides O(1) lookup of built-in crawlers by name.
var BuiltinByName map[string]*CrawlerInfo

func init() {
	BuiltinByName = make(map[string]*CrawlerInfo, len(BuiltinCrawlers))
	for _, c := range BuiltinCrawlers {
		BuiltinByName[c.Name] = c
	}
}
