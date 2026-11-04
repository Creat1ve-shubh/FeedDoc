// Vercel function that fetches a Shopify store's public pages for the web
// checker. Browsers cannot read another site's /products.json directly
// (no CORS headers), so the page asks this function instead.
//
//   GET /api/scan?store=example.com  ->  storefront.Input JSON (see internal/storefront)
//
// It only ever fetches fixed public paths on the host it is given, over
// https, and refuses IP addresses and local names. Vercel's CDN caches each
// store's answer for 10 minutes.

const MAX_PAGES = 10; // 250 products a page, so up to 2,500 products
const MAX_HTML = 400_000; // characters kept from each page, after scripts and styles are stripped
const POLICIES = {
  refund: "/policies/refund-policy",
  shipping: "/policies/shipping-policy",
  privacy: "/policies/privacy-policy",
  terms: "/policies/terms-of-service",
};

export async function GET(request) {
  const url = new URL(request.url);
  const host = normalizeHost(url.searchParams.get("store") || "");
  if (!host) return json({ error: "Enter a store address like yourstore.com or yourstore.myshopify.com." }, 400);
  try {
    return json(await collect(host), 200, { "Cache-Control": "public, s-maxage=600, stale-while-revalidate=60" });
  } catch (err) {
    return json({ error: err.message || String(err) }, 502);
  }
}

async function collect(host) {
  const base = `https://${host}`;
  const first = await getJSON(`${base}/products.json?limit=250&page=1`);
  if (!first || !Array.isArray(first.products)) {
    throw new Error(`${host} does not look like a Shopify store, or it hides its product list.`);
  }
  const pages = [first];
  let truncated = false;
  for (let p = 2; first.products.length === 250; p++) {
    if (p > MAX_PAGES) {
      truncated = true;
      break;
    }
    const page = await getJSON(`${base}/products.json?limit=250&page=${p}`);
    if (!page || !Array.isArray(page.products) || page.products.length === 0) break;
    pages.push(page);
    if (page.products.length < 250) break;
  }

  const [meta, home, ...policyStatus] = await Promise.all([
    getJSON(`${base}/meta.json`),
    getText(`${base}/`),
    ...Object.values(POLICIES).map((path) => status(`${base}${path}`)),
  ]);
  // Themes name the contact page differently (contact, contact-us), so
  // follow the home page's own link when it has one.
  const contactPath = contactLink(home) || "/pages/contact";
  const contact = await getText(`${base}${contactPath}`);
  const policies = {};
  Object.keys(POLICIES).forEach((k, i) => (policies[k] = policyStatus[i] === 200));

  return {
    domain: host,
    meta,
    productPages: pages,
    policies,
    homeHtml: home,
    contactHtml: contact,
    truncated,
  };
}

function normalizeHost(input) {
  let s = input.trim().toLowerCase();
  s = s.replace(/^https?:\/\//, "").split(/[/?#]/)[0].replace(/:\d+$/, "");
  if (!/^[a-z0-9.-]+\.[a-z]{2,}$/.test(s)) return "";
  if (/^\d+\.\d+\.\d+\.\d+$/.test(s) || s.endsWith(".local") || s.endsWith(".internal")) return "";
  return s;
}

const UA = { "User-Agent": "FeedDoctor-checker/1.0 (+https://github.com/Creat1ve-shubh/FeedDoc)" };

async function getJSON(url) {
  const r = await fetch(url, { headers: { ...UA, Accept: "application/json" }, redirect: "follow" });
  if (!r.ok) return null;
  try {
    return await r.json();
  } catch {
    return null;
  }
}

async function getText(url) {
  const r = await fetch(url, { headers: UA, redirect: "follow" });
  if (!r.ok) return "";
  // Scripts and styles fill most of a theme's HTML and would push the
  // footer, where contact details live, past the cap.
  const html = (await r.text()).replace(/<script\b[\s\S]*?<\/script>/gi, "").replace(/<style\b[\s\S]*?<\/style>/gi, "");
  return html.length > MAX_HTML ? html.slice(0, MAX_HTML / 4) + html.slice(-(MAX_HTML * 3) / 4) : html;
}

// contactLink returns the path of the first same-site link to a contact page.
function contactLink(html) {
  for (const m of html.matchAll(/href="(\/(?:[a-z]{2}(?:-[a-z]{2})?\/)?pages\/[^"#?]*contact[^"#?]*)"/gi)) {
    return m[1];
  }
  return "";
}

async function status(url) {
  const r = await fetch(url, { headers: UA, redirect: "follow" });
  return r.status;
}

function json(body, code, headers = {}) {
  return new Response(JSON.stringify(body), {
    status: code,
    headers: { "Content-Type": "application/json", ...headers },
  });
}
