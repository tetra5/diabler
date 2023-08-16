package events

import "time"

func NewWorldBossSchedule() *WorldBossSchedule {
	wb := &WorldBossSchedule{Length: 1000}
	wb.Init()
	return wb
}

type WorldBossSchedule struct {
	Entries       map[int]WorldBoss
	bossNames     map[int]string
	spawnPattern  []int
	minutePattern []float64
	lastSpawnIdx  int
	Length        int
}

type WorldBoss struct {
	Name      string
	SpawnTime time.Time // UTC
}

func (wbs *WorldBossSchedule) Init() {
	wbs.bossNames = map[int]string{
		1: "Wandering Death",
		2: "Avarice",
		3: "Ashava",
	}
	wbs.spawnPattern = []int{
		3, 3, 3, 2, 2, 1, 1, 1, 3, 3, 2, 2, 2, 1, 1,
	}

	wbs.minutePattern = []float64{353, 353.49, 325.71, 353.49, 325.22} // Repeats

	wbs.lastSpawnIdx = 0
	wbs.Entries = make(map[int]WorldBoss, wbs.Length)

	wbs.Entries[0] = WorldBoss{ // First ever WB spawn
		Name:      wbs.bossNames[1],
		SpawnTime: time.Date(2023, 6, 11, 6, 0, 0, 0, time.UTC),
	}

	pLen := len(wbs.spawnPattern)
	mLen := len(wbs.minutePattern)
	var bossName string
	for i, p, m := 1, 1, 1; i < wbs.Length; i++ {
		if m >= mLen {
			m = 0
		}
		t := wbs.Entries[i-1].SpawnTime.Add(time.Duration(wbs.minutePattern[m] * float64(time.Minute)))
		// Spawn time must belong to specific time intervals otherwise we add 2 hours
		// 04:30 - 06:30
		t1 := time.Date(t.Year(), t.Month(), t.Day(), 4, 30, 0, 0, t.Location())
		t2 := time.Date(t.Year(), t.Month(), t.Day(), 6, 30, 0, 0, t.Location())
		// 10:30 - 12:30
		t3 := time.Date(t.Year(), t.Month(), t.Day(), 10, 30, 0, 0, t.Location())
		t4 := time.Date(t.Year(), t.Month(), t.Day(), 12, 30, 0, 0, t.Location())
		// 16:30 - 18:30
		t5 := time.Date(t.Year(), t.Month(), t.Day(), 16, 30, 0, 0, t.Location())
		t6 := time.Date(t.Year(), t.Month(), t.Day(), 18, 30, 0, 0, t.Location())
		// 22:30 - 00:30
		t7 := time.Date(t.Year(), t.Month(), t.Day(), 22, 30, 0, 0, t.Location())
		t8 := time.Date(t.Year(), t.Month(), t.Day(), 0, 30, 0, 0, t.Location())

		if (t.After(t1) && t.Before(t2)) ||
			(t.After(t3) && t.Before(t4)) ||
			(t.After(t5) && t.Before(t6)) ||
			(t.After(t7) && t.After(t8)) {
		} else {
			t = t.Add(time.Duration(2 * time.Hour))
		}

		if p < pLen {
			bossName = wbs.bossNames[wbs.spawnPattern[p]]
		} else {
			bossName = wbs.Entries[i-15].Name // Spawn pattern repeats
		}
		wbs.Entries[i] = WorldBoss{
			Name:      bossName,
			SpawnTime: t,
		}
		p++
		m++
	}
}

func (wbs *WorldBossSchedule) Next() WorldBoss {
	now := time.Now().UTC()
	var i int
	for i = wbs.lastSpawnIdx; i < len(wbs.Entries); i++ {
		boss := wbs.Entries[i]
		if now.Before(boss.SpawnTime) {
			wbs.lastSpawnIdx = i - 1
			break
		}
	}
	return wbs.Entries[i]
}
