#!/usr/bin/env python3
"""Rebuild versioned initial SQL from the supplied 2026 schedules and inspected layouts.

Run only while authoring a new initial database, never to modify an applied migration.
"""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def seats(rows):
    return [{"label": str(n), "x": x, "y": y} for n, x, y in rows]


layouts = {
    "b1-male-general": seats([(1, 400, 80), (2, 480, 80), (3, 560, 80), (4, 640, 80)] +
        [(n, 820, y) for n, y in zip(range(5, 13), [150, 210, 300, 360, 450, 510, 600, 660])] +
        [(13, 560, 650), (14, 480, 650), (15, 560, 550), (16, 480, 550), (17, 560, 490), (18, 480, 490),
         (19, 560, 370), (20, 480, 370), (21, 560, 310), (22, 480, 310), (23, 400, 280), (24, 400, 350), (25, 400, 510), (26, 400, 590)] +
        [(n, 130, y) for n, y in zip(range(27, 33), [750, 680, 590, 520, 430, 350])]),
    "b1-female-general": seats([(53, 720, 95), (54, 820, 95), (55, 820, 270), (56, 720, 270),
        (57, 720, 340), (58, 820, 340), (59, 820, 500), (60, 720, 500), (61, 720, 570), (62, 820, 570),
        (63, 820, 760), (64, 720, 760), (65, 720, 830), (66, 820, 830),
        (67, 560, 700), (68, 560, 620), (69, 560, 480), (70, 560, 400),
        (71, 340, 240), (72, 440, 240), (73, 440, 430), (74, 340, 430), (75, 340, 500), (76, 440, 500),
        (77, 440, 700), (78, 340, 700), (79, 720, 920), (80, 820, 920),
        (81, 150, 550), (82, 150, 470), (83, 150, 350), (84, 150, 270)]),
    "b1-male-kupel": seats([(35, 720, 850), (36, 820, 850), (37, 820, 740), (38, 720, 740),
        (39, 720, 690), (40, 820, 690), (41, 820, 580), (42, 720, 580), (43, 720, 530), (44, 820, 530),
        (45, 820, 420), (46, 720, 420), (47, 720, 370), (48, 820, 370), (49, 820, 270), (50, 720, 270),
        (51, 150, 610), (52, 150, 550)]),
    "b1-female-kupel": seats([(85, 220, 620), (86, 120, 620), (87, 120, 470), (88, 220, 470),
        (89, 720, 65), (90, 820, 65), (91, 720, 220), (92, 820, 220), (93, 720, 280), (94, 820, 280),
        (95, 720, 440), (96, 820, 440), (97, 720, 600), (98, 820, 600), (99, 460, 460), (100, 460, 380),
        (111, 220, 410), (112, 120, 410)]),
    "b2-male-general": seats([(n, 820, 740 - (n - 1) * 65) for n in range(1, 11)] +
        [(11, 550, 80), (12, 450, 80), (13, 550, 200), (14, 550, 300),
         (15, 550, 450), (16, 550, 510), (17, 550, 570), (18, 550, 630),
         (19, 450, 630), (20, 450, 570), (21, 450, 510), (22, 450, 450),
         (23, 250, 170), (24, 250, 250), (25, 270, 620)]),
    "b2-female-general": seats([(1, 110, 800), (2, 110, 700), (3, 110, 520), (4, 220, 520),
        (5, 490, 520), (6, 490, 450), (7, 220, 450), (8, 110, 450),
        (9, 110, 300), (10, 260, 300), (11, 410, 300), (12, 560, 300), (13, 680, 270),
        (14, 560, 220), (15, 410, 220), (16, 260, 220), (17, 110, 220),
        (18, 110, 80), (19, 260, 80), (20, 820, 140), (21, 820, 270), (22, 820, 400), (23, 820, 530), (24, 820, 660)]),
}

departments = []
def department(identifier, bath, name, mode, gender="mixed", pool=False):
    departments.append(dict(id=identifier,bath_id=bath,name=name,mode=mode,gender=gender,pool=pool))

for bath in (1, 2):
    for gender, label in [("male", "Мужское"), ("female", "Женское")]:
        department(f"b{bath}-{gender}-general", bath, f"{label} общее", "session", gender)
        if bath == 1: department(f"b1-{gender}-kupel",1,f"{label} отделение «Купель»","session",gender)
    if bath == 1:
        for n in (1,2,3):department(f"b1-sauna-pool-{n}",1,f"Сауна с бассейном №{n}","sauna",pool=True)
        for n in (2,3):department(f"b1-sauna-floor-{n}",1,f"Сауна без бассейна, {n} этаж","sauna")
        department("b1-male-floor-1",1,"Мужское отделение первого этажа","info","male")
    else:
        department("b2-sauna-pool",2,"Сауна с бассейном","sauna",pool=True)
        department("b2-sauna",2,"Сауна без бассейна","sauna")
        department("b2-shower-large",2,"Большой душ, 10 кабинок","info")
        department("b2-shower-small",2,"Малый душ, 5 кабинок","info")

baths=[dict(id=1,name="Баня №1",address="Екатеринбург, ул. Первомайская, 71"),dict(id=2,name="Баня №2",address="Екатеринбург, ул. Лыжников, 38а")]
def sql(v):return "'"+str(v).replace("'","''")+"'"
def js(v):return sql(json.dumps(v,ensure_ascii=False,separators=(',',':')))+"::jsonb"

catalog=["INSERT INTO baths(id,name,address) VALUES "+','.join(f"({b['id']},{sql(b['name'])},{sql(b['address'])})" for b in baths)+";"]
for d in departments:
    description="Тестовое оформление. Реальные платежи и право фактического прохода не предоставляются."
    if d['mode']=='info':description+=" Предварительная продажа отсутствует; посещение и покупка на месте."
    catalog.append(f"INSERT INTO departments(id,bath_id,name,mode,description) VALUES({sql(d['id'])},{d['bath_id']},{sql(d['name'])},{sql(d['mode'])},{sql(description)});")

booking=[]
for d in departments:
    if d['mode']=='info':continue
    weekday=[3,4,5] if d['bath_id']==1 else [3,4]
    week={}
    def slots(pairs):return [dict(start=a,end=b,gender=d['gender']) for a,b in pairs]
    if d['mode']=='sauna':
        for day in weekday:week[str(day)]=slots([('09:00','23:00')] if d['bath_id']==1 else [('12:30','21:30')])
        for day in [6,0]:week[str(day)]=slots([('09:00','23:00')] if d['bath_id']==1 else [('09:00','21:00')])
    elif 'kupel' in d['id']:
        pairs=[('09:20','11:20'),('11:40','13:40'),('14:00','16:00'),('16:20','18:20'),('18:40','20:40'),('21:00','23:00')]
        for day in weekday+[6,0]:week[str(day)]=slots(pairs)
    elif d['bath_id']==1:
        for day in [3,4]:week[str(day)]=slots([('09:00','10:30'),('10:50','12:20'),('12:40','14:10'),('14:30','16:00'),('16:20','18:20'),('18:40','20:40'),('21:00','23:00')])
        for day in [5,6,0]:week[str(day)]=slots([('09:00','11:00'),('11:20','13:20'),('13:40','15:40'),('16:00','18:00'),('18:30','20:30'),('21:00','23:00')])
    else:
        for day in [3,4]:week[str(day)]=slots([('12:30','14:00'),('14:30','16:30'),('17:00','19:00'),('19:30','21:30')])
        for day in [6,0]:week[str(day)]=slots([('09:00','11:00'),('11:30','13:30'),('14:00','16:00'),('16:30','18:30'),('19:00','21:00')])
    schedule=dict(week=week,exceptions={},calendar={})
    rules=[]
    def rule(kind,start,end,adult=0,child=0,hourly=0,extra=0,duration=0):
        rules.append(dict(id=f"rule-{len(rules)+1}",valid_from="2026-09-30",valid_until="2100-01-01",day_types=[kind],start_minute=start,end_minute=end,duration_minutes=duration,adult_kopecks=adult,child_kopecks=child,hourly_kopecks=hourly,extra_hourly_kopecks=extra))
    if d['mode']=='sauna':
        low,high,exlow,exhigh = ((150000,190000,30000,38000) if d['pool'] else (120000,160000,24000,32000)) if d['bath_id']==1 else ((120000,160000,20000,27000) if d['pool'] else (100000,140000,20000,28000))
        rule('weekday',0,960,hourly=low,extra=exlow);rule('weekday',960,1440,hourly=high,extra=exhigh)
        for day in ['weekend','holiday']:rule(day,0,1440,hourly=high,extra=exhigh)
    elif 'kupel' in d['id']:
        rule('weekday',0,960,63000,31000,duration=120);rule('weekday',960,1440,69000,33000,duration=120)
        for day in ['weekend','holiday']:rule(day,0,1440,69000,33000,duration=120)
    else:
        for day in ['weekday','weekend','holiday']:
            rule(day,0,1440,32000,22000,duration=90);rule(day,0,1440,45000,30000,duration=120)
    base=6 if d['id']=='b2-sauna-pool' else 5
    bath=baths[d['bath_id']-1]
    booking.append(f"INSERT INTO departments(id,bath_id,bath_name,bath_address,name,mode,base_guests,max_guests,schedule,tariffs) VALUES({sql(d['id'])},{d['bath_id']},{sql(bath['name'])},{sql(bath['address'])},{sql(d['name'])},{sql(d['mode'])},{base},{10 if d['mode']=='sauna' else 200},{js(schedule)},{js(dict(rules=rules))});")
    layout=layouts.get(d['id'],[dict(label='whole',x=500,y=500)])
    for r in layout:
        booking.append(f"INSERT INTO resources(id,department_id,label,x,y) VALUES({sql(d['id']+':'+r['label'])},{sql(d['id'])},{sql(r['label'])},{r['x']},{r['y']});")

(ROOT/'seed/catalog.json').write_text(json.dumps(dict(baths=baths,departments=departments,layouts=layouts),ensure_ascii=False,indent=2)+'\n')
(ROOT/'services/catalog/migrations/000002_seed.up.sql').write_text('\n'.join(catalog)+'\n')
(ROOT/'services/catalog/migrations/000002_seed.down.sql').write_text('DELETE FROM departments;\nDELETE FROM baths;\n')
(ROOT/'services/booking/migrations/000002_seed.up.sql').write_text('\n'.join(booking)+'\n')
(ROOT/'services/booking/migrations/000002_seed.down.sql').write_text('DELETE FROM resources;\nDELETE FROM departments;\n')
print(f'Prepared {len(departments)} departments and {sum(map(len,layouts.values()))} explicitly numbered seats.')
