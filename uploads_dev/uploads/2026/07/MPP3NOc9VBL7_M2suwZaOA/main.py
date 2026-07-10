import os
import re
import httpx
from fastapi import FastAPI, HTTPException, Response
from fastapi.middleware.cors import CORSMiddleware
from dotenv import load_dotenv
from pydantic import BaseModel
from typing import List, Optional

load_dotenv()
TAVILY_API_KEY = os.getenv("TAVILY_API_KEY")
if not TAVILY_API_KEY:
    raise ValueError("System Error: TAVILY_API_KEY is missing from your .env file.")

app = FastAPI(title="Social Citation AI Search Engine")

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

class SearchRequest(BaseModel):
    keyword: str

class CitationResult(BaseModel):
    title: str
    url: str
    snippet: str
    platform: str

class SearchResponse(BaseModel):
    query_used: str
    results: List[CitationResult]

PLATFORMS = {
    "Reddit": {
        "domains": ["reddit.com"],
        "url_pattern": re.compile(r"reddit\.com/.*/comments/", re.IGNORECASE),
        "generic_blockers": [
            re.compile(r"reddit\.com/r/[^/]+/?$", re.IGNORECASE),
            re.compile(r"reddit\.com/?$", re.IGNORECASE),
        ],
    },
    "X (Twitter)": {
        "domains": ["x.com", "twitter.com"],
        "url_pattern": re.compile(r"(x\.com|twitter\.com)/[^/]+/status/", re.IGNORECASE),
        "generic_blockers": [
            re.compile(r"(x\.com|twitter\.com)/(home|explore|i|search|notifications|messages|compose)/?", re.IGNORECASE),
        ],
    },
    "Instagram": {
        "domains": ["instagram.com"],
        "url_pattern": re.compile(r"instagram\.com/(reel|p|tv)/", re.IGNORECASE),
        "generic_blockers": [
            re.compile(r"instagram\.com/[^/]+/?$", re.IGNORECASE),
            re.compile(r"instagram\.com/?$", re.IGNORECASE),
        ],
    },
}

def normalize_text(value: str) -> str:
    return re.sub(r"[^a-z0-9]+", "", value.lower())

def normalize_words(value: str) -> List[str]:
    matches = re.findall(r"[a-z0-9]+", value.lower())
    terms = []

    for term in matches:
        if len(term) > 2:
            terms.append(term)
    return terms

def is_query_specific(keyword: str, title: str, snippet: str, url: str) -> bool:
    query_key = normalize_text(keyword)
    if not query_key:
        return False

    ntext = normalize_text(f"{title} {snippet} {url}")
    if query_key in ntext:
        return True

    query_terms = normalize_words(keyword)
    if not query_terms:
        return False

    matched_terms = 0
    for term in query_terms:
        if term in ntext:
            matched_terms += 1
    return matched_terms >= max(1, len(query_terms) - 1)

def is_generic_platform_page(platform: str, url: str) -> bool:
    lowered_url = url.lower()
    rules = PLATFORMS[platform]
    for pattern in rules["generic_blockers"]:
        if pattern.search(lowered_url):
            return True
    return False

def get_platform_from_url(url: str) -> Optional[str]:
    lowered_url = url.lower()
    if "reddit.com" in lowered_url:
        return "Reddit"
    if "x.com" in lowered_url or "twitter.com" in lowered_url:
        return "X (Twitter)"
    if "instagram.com" in lowered_url:
        return "Instagram"
    return None

def build_tavily_query(keyword: str, platform: str) -> str:
    domains = []
    for domain in PLATFORMS[platform]["domains"]:
        domains.append(f"site:{domain}")
    domains = " OR ".join(domains)

    return f'"{keyword}" ({domains})'

def build_multi_domain_payload(keyword: str) -> dict:
    all_domains = []
    for rules in PLATFORMS.values():
        all_domains.extend(rules["domains"])

    all_domains = list(dict.fromkeys(all_domains))

    payload = {
        "query": f'"{keyword}"',
        "search_depth": "basic",
        "include_answer": False,
        "include_raw_content": "text",
        "include_domains": all_domains,
        "exact_match": True,
        "max_results": 20,
    }

    return payload

DELETED_MARKERS = re.compile(
    r"(removed|deleted|this content is not available|page not found|post unavailable|content unavailable|sorry, this page isn't available|page isn't available|the page you were looking for cannot be found|post isn't available|post isnt available|the link may be broken|profile may have been removed|this profile is not available|this page isn't available|this page isnt available|video unavailable|this video is unavailable|tweet is unavailable|this tweet is unavailable)",
    re.IGNORECASE,
)

def content_is_deleted_or_blocked(text: str) -> bool:
    if not text:
        return False
    if DELETED_MARKERS.search(text):
        return True
    if re.search(r"\[(deleted|removed)\]", text, re.IGNORECASE):
        return True
    if re.search(r"sign up for instagram|create an account", text, re.IGNORECASE):
        return False
    return False

async def url_is_live(client: httpx.AsyncClient, url: str) -> bool:
    try:
        response = await client.get(
            url,
            follow_redirects=True,
            timeout=15.0,
            headers={
                "User-Agent": (
                    "Mozilla/5.0 (X11; Linux x86_64) "
                    "AppleWebKit/537.36 "
                    "(KHTML, like Gecko) "
                    "Chrome/137.0 Safari/537.36"
                )
            },
        )

        if response.status_code in {404, 410, 451}:
            return False

        if response.status_code >= 500:
            return False

        page_text = response.text or ""
        if content_is_deleted_or_blocked(page_text):
            return False

        lowered_text = page_text.lower()
        if "sign up for instagram" in lowered_text and "post isn't available" in lowered_text:
            return False
        if "sign up for instagram" in lowered_text and "the link may be broken" in lowered_text:
            return False

        return True
    except httpx.HTTPError:
        return False
    except Exception:
        return False

async def fetch_page_text(client: httpx.AsyncClient, url: str) -> str:
    try:
        resp = await client.get(url, follow_redirects=True, timeout=10.0)
        resp.raise_for_status()
        return resp.text[:20000]
    except Exception:
        return ""

@app.post("/search", response_model=SearchResponse)
async def aggregate_social_search(request: SearchRequest, response_format: str = "text"):
    if not request.keyword.strip():
        raise HTTPException(status_code=400, detail="Search keyword cannot be empty.")

    target_query = f'"{request.keyword}" (site:reddit.com OR site:x.com OR site:instagram.com)'

    try:
        async with httpx.AsyncClient() as client:
            payload = build_multi_domain_payload(request.keyword)
            response = await client.post(
                "https://api.tavily.com/search",
                json=payload,
                headers={"Authorization": f"Bearer {TAVILY_API_KEY}"},
                timeout=20.0,
            )
            response.raise_for_status()
            data = response.json()
    except httpx.HTTPStatusError as e:
        raise HTTPException(status_code=e.response.status_code, detail="External Search Engine Error.")
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Network Processing Error: {str(e)}")

    candidates = []
    async with httpx.AsyncClient() as validator_client:
        for item in data.get("results", []):
            url = item.get("url", "")
            platform = get_platform_from_url(url)
            if not platform:
                continue

            if not await url_is_live(validator_client, url):
                continue

            snippet = item.get("content") or item.get("raw_content") or ""
            if content_is_deleted_or_blocked(snippet):
                continue

            if not snippet:
                page_text = await fetch_page_text(validator_client, url)
                if content_is_deleted_or_blocked(page_text):
                    continue

            title = item.get("title", "Social Media Update")
            if not is_query_specific(request.keyword, title, snippet, url):
                continue

            if is_generic_platform_page(platform, url):
                continue

            candidates.append(
                {
                    "platform": platform,
                    "title": title,
                    "url": url,
                    "snippet": item.get("content", "No preview available."),
                }
            )

    selected_results = []
    seen_urls = set()
    for candidate in candidates:
        if candidate["url"] in seen_urls:
            continue

        seen_urls.add(candidate["url"])
        selected_results.append(
            CitationResult(
                title=candidate["title"],
                url=candidate["url"],
                snippet=candidate["snippet"],
                platform=candidate["platform"],
            )
        )

        if len(selected_results) >= 10:
            break

    if response_format and response_format.lower() in ("text", "plain", "txt") and not selected_results:
        return Response(status_code=204)

    if response_format and response_format.lower() in ("text", "plain", "txt"):
        platform_order = ["Reddit", "X (Twitter)", "Instagram"]
        groups = {p: [] for p in platform_order}
        for r in selected_results:
            groups.get(r.platform, []).append(r)

        lines = []
        for p in platform_order:
            items = groups.get(p, [])
            if not items:
                continue
            lines.append(f"{p}:")
            for i in items:
                lines.append(f"{p}: {i.url}")
            lines.append("")

        return Response("\n".join(lines).strip(), media_type="text/plain")

    return SearchResponse(query_used=target_query, results=selected_results)
