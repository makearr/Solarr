# Benchmarks the TV metadata providers (latency, parallel throughput); pass a JSON sample list of [title, tvdb id, tmdb id, tvmaze id].
import json,re,time,statistics,sys,math,urllib.request,urllib.error,concurrent.futures as cf
from pathlib import Path
if len(sys.argv) != 2:
    raise SystemExit(f"usage: {Path(sys.argv[0]).name} <sample-series.json>")
sec=(Path(__file__).resolve().parents[1]/"private/secrets.md").read_text()
TMDB=re.search(r"TMDB read access token \(v4, bearer\) \| `([^`]+)`",sec).group(1)
TVDBKEY=re.search(r"TVDB v4 \(new public project key\) \| `([^`]+)`",sec).group(1)
rows=json.loads(Path(sys.argv[1]).read_text())
UA={"User-Agent":"Solarr-bench/1.0"}
def get(url,headers=None,data=None,method=None,timeout=40):
    req=urllib.request.Request(url,headers={**UA,**(headers or {})},data=data,method=method)
    t=time.time()
    try:
        with urllib.request.urlopen(req,timeout=timeout) as r:
            b=r.read(); return time.time()-t,r.status,b
    except urllib.error.HTTPError as e:
        return time.time()-t,e.code,e.read()
    except Exception as e:
        return time.time()-t,0,str(e).encode()
# tvdb login
t,s,b=get("https://api4.thetvdb.com/v4/login",{"Content-Type":"application/json"},json.dumps({"apikey":TVDBKEY}).encode(),"POST")
TVDBTOK=json.loads(b)["data"]["token"] if s==200 else None
print("tvdb login",s,round(t,2))
H_TMDB={"Authorization":"Bearer "+TMDB}
H_TVDB={"Authorization":"Bearer "+(TVDBTOK or "")}
providers={
 "skyhook":lambda r:f"https://skyhook.sonarr.tv/v1/tvdb/shows/en/{r[1]}",
 "tvmaze":lambda r:f"https://api.tvmaze.com/lookup/shows?thetvdb={r[1]}",
 "tmdb":lambda r:f"https://api.themoviedb.org/3/tv/{r[2]}?append_to_response=credits,external_ids,content_ratings,images",
 "tvdb":lambda r:f"https://api4.thetvdb.com/v4/series/{r[1]}/extended?short=false",
}
heads={"tmdb":H_TMDB,"tvdb":H_TVDB}
def one(name,r):
    if name=="tmdb" and not r[2]: return None
    return get(providers[name](r),heads.get(name))
print("== single request latency (sequential, 20 series)")
bodies={}
for name in providers:
    lat=[];bad=0
    for r in rows[:20]:
        res=one(name,r)
        if res is None: continue
        lat.append(res[0]); 
        if res[1]!=200: bad+=1
        else: bodies[(name,r[1])]=res[2]
    lat.sort()
    if lat:
        p90=lat[math.ceil(len(lat)*0.9)-1]
        print(f"{name:8s} n={len(lat)} median={statistics.median(lat):.2f}s p90={p90:.2f}s max={lat[-1]:.2f}s errors={bad}")
    else:
        print(f"{name:8s} n=0 median=n/a p90=n/a max=n/a errors={bad}")
print("== parallel throughput (40 requests)")
for name in providers:
    for conc in (1,4,8,16):
        t0=time.time();ok=0;codes={}
        with cf.ThreadPoolExecutor(conc) as ex:
            for res in ex.map(lambda r:one(name,r),rows):
                if res is None: continue
                codes[res[1]]=codes.get(res[1],0)+1
        dt=time.time()-t0
        n=sum(codes.values())
        print(f"{name:8s} conc={conc:2d} {n/dt:5.1f} req/s  codes={codes}")
json.dump({f"{k[0]}|{k[1]}":v.decode("utf8","replace") for k,v in bodies.items()},open("bodies.json","w"))
