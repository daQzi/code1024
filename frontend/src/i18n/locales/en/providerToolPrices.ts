export default {
  providerToolPrices: {
    title: 'Provider tool sale prices',
    hint: 'JSON map in USD/1K calls. Keys are tool names or tool:model-prefix*. The longest model prefix wins, and numeric 0 explicitly means free. Up to 512 entries, with prices from 0 to one trillion USD/1K calls.',
    fallback: 'These are site sale prices. Vendor prices and exchange rates are not imported. Unmatched web_search / web_search_preview use an explicit group search price; other positive counts require a configured price. Each request captures its prices at admission. Updates apply to later requests.',
    prices: 'Tool price map (USD/1K calls)',
    saved: 'Tool prices saved for subsequent requests.',
    loadFailed: 'Unable to load tool prices. Please retry.',
    saveFailed: 'Unable to save tool prices. Your draft is preserved.',
    conflict: 'Another administrator changed these prices. Your draft is preserved. Copy it before refreshing and reviewing the changes.',
    invalid: 'Enter a valid JSON object with valid tool names or model prefixes and finite non-negative prices up to one trillion USD. Up to 512 entries; ambiguous prefixes are not allowed.',
  }
}
