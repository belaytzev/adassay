package extract

import (
	"strings"
	"testing"
)

func TestHiddenSkipsMenusAndShortDescriptions(t *testing.T) {
	const src = `<html><body>
<nav aria-hidden="true"><ul>
<li><a href="/research">Research</a></li><li><a href="/products">Products</a></li>
<li><a href="/chatgpt">Try ChatGPT<span> (opens in a new window)</span></a></li><li><a href="/login">Login</a></li>
</ul></nav>
<div class="shortdescription" style="display:none">Software feature removing online advertising in a web browser or application</div>
<article><p>Обычный абзац статьи про блокировку рекламы.</p></article>
</body></html>`
	found, err := Hidden(strings.NewReader(src), testL1(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("site chrome reported as hidden text: %+v", found)
	}
}
