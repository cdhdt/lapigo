# Notes de brainstorming — cache, pagination, budget

> Notes de travail (pas encore un spec). Décisions à consigner dans le spec de phase 1.

## Décisions arrêtées

1. **Générateur de code**, pas runtime.
2. Source de vérité : **fichier YAML dédié** (`lapigo.yaml`).
3. **Séparation stricte** : `internal/gen/` réécrit intégralement, extension par hooks.
4. **Mono-stack stdlib** : `net/http` (routage Go 1.22+) + `pgx` + Postgres.
5. Périmètre : CRUD, pagination curseur, filtres, tri, migrations, auth JWT,
   OpenAPI, cache Redis (optionnel, phase 5).
6. **Écrit de zéro**, pas de fork PocketBase.
7. **Curseur seul** : pas de numéro de page, pas de total, pas de `COUNT(*)`.
   L'API expose `?limit=` et `?after=<curseur>`, et renvoie `next`.
   Conséquence : l'index de bornes (ZSET) sert *uniquement* à l'invalidation de
   cache, pas à la navigation.

### Pourquoi pas un fork de PocketBase

Six composants sur sept seraient à supprimer (admin UI, realtime, file storage,
collections dynamiques, couche SQLite, modèle records). Seul le parseur de
filtres est inspirant, et il se copie sans forker.

Preuve empirique : PocketBase est à **v0.39.11 (14 août 2026)**, poussé
quotidiennement, sans garantie de compatibilité ascendante avant la v1.0. Les
forks Postgres (`postgresbase`, quatre organisations) sont tous figés à
**v0.20.5 / v0.22.19**, encore marqués « Building… ». Dix-sept à dix-neuf
versions de retard : maintenir un fork divergent de PocketBase ne fonctionne
pour personne.

À copier quand même : la syntaxe de filtres, les règles d'accès déclaratives par
endpoint, la DX « un binaire, tu lances, ça marche ».

## Contexte de charge cible

- Plusieurs milliers de req/min sur les endpoints de liste (~100–350 req/s).
- Plusieurs écritures par minute sur les mêmes collections.
- Plusieurs milliers d'utilisateurs simultanés.
- Budget serveur : 15–20 €/mois, plafond 4 vCPU / 8 Go RAM.

## Décision : invalidation de cache par plage de clés (pas par collection)

**Rejeté** : le compteur de version par collection (`INCR article:ver`). Il purge
tout, y compris les pages profondes que personne n'a modifiées depuis des
semaines. Inacceptable à ce profil de charge.

**Retenu** : chaque page cachée couvre une plage fermée de clés de tri
`[k_max, k_min]`. Une écriture sur la ligne X n'invalide que la page dont la
plage contient `k_X`.

### Structure Redis

Un ZSET par couple (entité, jeu de filtres) :

```
article:pages:{filterhash}
  membre = clé de cache de la page
  score  = borne basse de la page (k_min)
```

Sur écriture de la ligne X de clé de tri `k_X` :

```
ZRANGEBYSCORE article:pages:{f} k_X +inf LIMIT 0 1
  -> la page dont k_min est le plus petit >= k_X
  -> c'est la page qui contient k_X
DEL <cette clé> ; ZREM <cette clé>
```

Coût `O(log N)`, **une seule page invalidée**, aucun `SCAN`.

### Conséquences

- Éditer un article de la page 4 purge la page 4. La page 5 reste en cache.
- La page 3000 a un `k_min` vieux de trois semaines ; aucune écriture récente
  n'a de clé dans sa plage. Elle n'est **jamais** invalidée.
- Un update qui change le champ de tri ou un champ filtré = suppression d'une
  plage + insertion dans une autre : deux pages invalidées.
- Une insertion en tête (clé plus grande que toutes) ne tombe dans aucune plage :
  elle vise la **page de tête** (requête sans curseur), traitée à part.

### La page de tête est le seul point chaud

C'est la seule page qui churne. Traitement : TTL très court (1–5 s) +
single-flight (`SET NX`) + stale-while-revalidate. À 3 000 req/min sur la page 1
avec un post toutes les 20 s, un TTL de 2 s donne encore ~99 % de hit ratio et
au plus 0,5 requête DB/seconde.

## Propriété clé : un cache indexé par curseur ne peut pas être faux

La clé de cache contient le curseur, qui est une **position absolue** dans
l'ordre de tri. Si une insertion modifie le contenu d'une page, la page suivante
change de curseur, donc de clé de cache : c'est un *miss*, jamais une donnée
périmée servie. Les anciennes entrées meurent par TTL.

Cette propriété n'existe pas avec l'offset : `page=5` désigne un contenu
différent après chaque insertion, donc une entrée cachée devient silencieusement
fausse. **C'est la raison profonde de choisir le curseur, avant même la
performance.**

## Garde-fous

- Whitelist des combinaisons de filtres cachables (sinon `?page=99999` en boucle
  pollue Redis, et le nombre de ZSET explose).
- Jamais de cache par défaut sur une route authentifiée. Opt-in explicite, avec
  l'identité ou le rôle dans la clé. Sinon fuite de données entre utilisateurs.
- `limit` plafonné par la génération.

## Ce qui tue réellement un 4 vCPU à cette charge (par ordre d'impact)

1. **`COUNT(*)` sur chaque liste.** Mesuré chez PocketBase : 3,8–4,6 s avec
   count contre 9–110 ms sans, sur 50 k enregistrements avec relations. Facteur 40.
   La pagination par curseur n'a pas de COUNT du tout.
2. **`OFFSET` profond.** `OFFSET 60000` scanne 60 000 lignes. Le curseur fait un
   seek d'index : la page 3000 coûte le même prix que la page 1.
3. **N+1 sur les relations.** À générer en JOIN ou en batch `IN`, jamais en
   boucle.
4. **Épuisement du pool de connexions.** `pgxpool` dimensionné ~4 × cœurs.
5. **`limit` non plafonné.**

Les points 1, 2, 3 et 5 sont éliminables **par construction** dans un
générateur. C'est l'argument central du projet : un générateur ne peut pas
écrire de N+1 par distraction.

## Conséquence sur le rôle du cache

À 100–350 req/s, une requête keyset indexée coûte ~0,5 ms : environ 15 % d'un
cœur. **Postgres seul suffit.** Redis n'est pas ce qui sauve cette charge — le
curseur et l'absence de COUNT le sont.

Redis reste justifié pour : absorber les pics, les filtres coûteux, les
agrégats, et se donner de la marge d'un facteur 10. Mais il est
**optionnel par design**, pas un prérequis. Le code généré doit tourner
correctement sans Redis.

## Budget

Hetzner CPX31 ou équivalent (4 vCPU, 8 Go, NVMe) ≈ 15–16 €/mois.
Répartition : Postgres `shared_buffers` 2 Go / `effective_cache_size` 6 Go,
Redis `maxmemory` 512 Mo – 1 Go, application 200–500 Mo.

Limite acceptée : une seule machine, donc pas de haute disponibilité. Mais le
code généré est **sans état** et Postgres est externalisable : passer à
plusieurs instances + PG managé se fait sans réécriture. PocketBase ne le permet
pas (SQLite embarqué).
