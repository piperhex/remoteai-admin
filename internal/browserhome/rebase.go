package browserhome

import (
	"bytes"
	"io"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

const sitePrefix = "/browser-home/site/"

var rootReference = regexp.MustCompile("[\"'`(](/[^\"'`\\s)<>\\\\]*)")

// Rebase references to files that actually exist in this upload. External URLs and API paths stay intact.
func rebaseAsset(asset Asset, assets []Asset) []byte {
	if !strings.HasPrefix(asset.ContentType, "text/") {
		return asset.Data
	}
	paths := make(map[string]bool, len(assets))
	for _, item := range assets {
		paths[item.Path] = true
	}
	prefix := sitePrefix + asset.Revision + "/"
	rebased := rootReference.ReplaceAllStringFunc(string(asset.Data), func(match string) string {
		reference := match[1:]
		parsed, err := url.Parse(reference)
		if err != nil || parsed.Host != "" || !paths[strings.TrimPrefix(parsed.Path, "/")] {
			return match
		}
		return match[:1] + prefix + strings.TrimPrefix(reference, "/")
	})
	if strings.HasPrefix(asset.ContentType, "text/html") {
		return rebaseHTML([]byte(rebased), prefix)
	}
	return []byte(rebased)
}

func rebaseHTML(data []byte, prefix string) []byte {
	tokens := html.NewTokenizer(bytes.NewReader(data))
	var output strings.Builder
	baseWritten := false
	for {
		kind := tokens.Next()
		if kind == html.ErrorToken {
			if tokens.Err() != io.EOF {
				return data
			}
			break
		}
		token := tokens.Token()
		if token.Data == "base" && (kind == html.StartTagToken || kind == html.SelfClosingTagToken) {
			continue
		}
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			// Asset rebasing changes script/style bytes, so old build-time integrity digests no longer apply.
			attrs := token.Attr[:0]
			for _, attr := range token.Attr {
				if attr.Key != "integrity" {
					attrs = append(attrs, attr)
				}
			}
			token.Attr = attrs
		}
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			output.WriteString(token.String())
		} else {
			output.Write(tokens.Raw())
		}
		if kind == html.StartTagToken && token.Data == "head" {
			output.WriteString(`<base href="` + prefix + `">`)
			output.WriteString(linkBridge(prefix))
			baseWritten = true
		}
	}
	if !baseWritten {
		return []byte(`<base href="` + prefix + `">` + linkBridge(prefix) + output.String())
	}
	return []byte(output.String())
}

func linkBridge(prefix string) string {
	return `<script>(function(){
document.addEventListener('click', function(event){
  var anchor = event.target.closest && event.target.closest('a[href]');
  if (!anchor || event.defaultPrevented || anchor.hasAttribute('download')) return;
  var url = new URL(anchor.href, document.baseURI);
  if (!/^https?:$/.test(url.protocol)) return;
  if (url.origin === location.origin && url.pathname.startsWith('` + prefix + `')) return;
  event.preventDefault();
  parent.postMessage({type:'remoteai:browser-home:navigate',url:url.href,
    newTab:anchor.target === '_blank' || event.ctrlKey || event.metaKey}, '*');
});
})();</script>`
}
