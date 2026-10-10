-- What kind of file each media object is (audio, video, subtitle, image, other), set when it is registered (revision
-- 2, D3): "is it video" was twelve LIKE terms on its path. NULL is not yet classified; the existing rows are classified
-- by filekind's table at start, a page at a time.
ALTER TABLE media_object ADD COLUMN kind TEXT;
