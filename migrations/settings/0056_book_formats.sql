-- Ebook formats a library may hold grew (EPUB3, comics, HTML, PDB, PML), and CBZ ranks beside EPUB as the preferred ones.
UPDATE quality_profile
SET rules_json = '{"quality_profile":"Ebook: EPUB preferred","compare":["extension","score"],"tiers":{"extension":[["epub","epub3","cbz"],["azw3","kfx"],["mobi","azw"],["pdf","cbr"],["fb2","djvu","lit","pdb","pml","rtf","txt","html","htm","docx"]]},"scoring":[{"name":"Retail","score":5,"specs":[{"field":"title","matches":"(?i)\\bretail\\b","required":true}]}]}'
WHERE name = 'Ebook: EPUB preferred' AND media_type = 'book'
  AND rules_json = '{"quality_profile":"Ebook: EPUB preferred","compare":["extension","score"],"tiers":{"extension":[["epub"],["azw3","kfx"],["mobi","azw"],["pdf"],["fb2","djvu","lit","rtf","txt","docx"]]},"scoring":[{"name":"Retail","score":5,"specs":[{"field":"title","matches":"(?i)\\bretail\\b","required":true}]}]}';
